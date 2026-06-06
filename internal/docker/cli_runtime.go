package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	aerrors "devex-agent/internal/errors"
)

const (
	labelManaged      = "devex.managed"
	labelManagedValue = "true"

	maxStderrLog = 500 // truncate stderr in logs/errors to avoid noise
)

// CLIRuntime implements Runtime using the Docker CLI via a CommandExecutor.
type CLIRuntime struct {
	command  string // docker binary, typically "docker"
	executor CommandExecutor
	logger   *slog.Logger
}

// NewCLIRuntime creates a CLIRuntime.
// command is the Docker CLI binary (defaults to "docker" when empty).
func NewCLIRuntime(command string, executor CommandExecutor, logger *slog.Logger) *CLIRuntime {
	if command == "" {
		command = "docker"
	}
	return &CLIRuntime{command: command, executor: executor, logger: logger}
}

// PullImage pulls a Docker image.
// Returns CodeImageNotFound when the image is not found in the registry,
// CodeImagePullFailed for other pull failures.
func (r *CLIRuntime) PullImage(ctx context.Context, image string) error {
	if err := validateImageName(image); err != nil {
		return err
	}
	r.logger.Debug("pulling image", "image", image)

	res, err := r.executor.Run(ctx, r.command, "pull", image)
	if err != nil {
		return r.execErr(err, aerrors.CodeImagePullFailed, "docker.pull")
	}
	if res.ExitCode != 0 {
		return r.classifyPullError(res.Stderr, image)
	}

	r.logger.Info("image pulled successfully", "image", image)
	return nil
}

// StartContainer creates and starts a container from spec.
// The HostPort must come from the Port Manager; it is never chosen here.
// Env var values are never logged.
func (r *CLIRuntime) StartContainer(ctx context.Context, spec ContainerSpec) (*ContainerInfo, error) {
	if err := validateContainerName(spec.Name); err != nil {
		return nil, err
	}
	if err := validateImageName(spec.Image); err != nil {
		return nil, err
	}

	args := r.buildRunArgs(spec)

	r.logger.Debug("starting container",
		"name", spec.Name,
		"image", spec.Image,
		"host_port", spec.HostPort,
		"container_port", spec.ContainerPort,
	)

	res, err := r.executor.Run(ctx, r.command, args...)
	if err != nil {
		return nil, r.execErr(err, aerrors.CodeContainerStartFailed, "docker.run")
	}
	if res.ExitCode != 0 {
		return nil, r.classifyRunError(res.Stderr, spec.Name)
	}

	containerID := strings.TrimSpace(res.Stdout)
	r.logger.Info("container started", "name", spec.Name, "image", spec.Image, "id", truncate(containerID, 12))

	return &ContainerInfo{
		ID:            containerID,
		Name:          spec.Name,
		Image:         spec.Image,
		Status:        "running",
		Running:       true,
		HostPort:      spec.HostPort,
		ContainerPort: spec.ContainerPort,
		Labels:        spec.Labels,
		CreatedAt:     time.Now().UTC(),
	}, nil
}

// StopContainer gracefully stops a container within the given timeout.
func (r *CLIRuntime) StopContainer(ctx context.Context, name string, timeout time.Duration) error {
	secs := int(timeout.Seconds())
	if secs <= 0 {
		secs = 10
	}

	r.logger.Debug("stopping container", "name", name, "timeout_secs", secs)

	res, err := r.executor.Run(ctx, r.command, "stop", "--time", strconv.Itoa(secs), name)
	if err != nil {
		return r.execErr(err, aerrors.CodeContainerStopFailed, "docker.stop")
	}
	if res.ExitCode != 0 {
		if isNotFound(res.Stderr) {
			return aerrors.Newf(aerrors.CodeContainerNotFound, "container %q not found", name)
		}
		return aerrors.Newf(aerrors.CodeContainerStopFailed,
			"docker stop %q failed: %s", name, truncate(res.Stderr, maxStderrLog))
	}

	r.logger.Info("container stopped", "name", name)
	return nil
}

// RemoveContainer removes a container after verifying it has the devex.managed=true label.
// Returns CodeContainerNotManaged if the container is not managed by the DevEx Agent.
func (r *CLIRuntime) RemoveContainer(ctx context.Context, name string) error {
	// Verify managed label before removal (safety check).
	info, err := r.InspectContainer(ctx, name)
	if err != nil {
		return err // CodeContainerNotFound or CodeContainerInspectFailed
	}
	if info.Labels[labelManaged] != labelManagedValue {
		return aerrors.Newf(aerrors.CodeContainerNotManaged,
			"container %q does not have label %s=%s; refusing to remove",
			name, labelManaged, labelManagedValue)
	}

	r.logger.Debug("removing container", "name", name)

	res, err := r.executor.Run(ctx, r.command, "rm", name)
	if err != nil {
		return r.execErr(err, aerrors.CodeContainerRemoveFailed, "docker.rm")
	}
	if res.ExitCode != 0 {
		if isNotFound(res.Stderr) {
			return aerrors.Newf(aerrors.CodeContainerNotFound, "container %q not found", name)
		}
		return aerrors.Newf(aerrors.CodeContainerRemoveFailed,
			"docker rm %q failed: %s", name, truncate(res.Stderr, maxStderrLog))
	}

	r.logger.Info("container removed", "name", name)
	return nil
}

// InspectContainer returns detailed runtime info about a container.
// Returns CodeContainerNotFound when the container does not exist.
func (r *CLIRuntime) InspectContainer(ctx context.Context, name string) (*ContainerInfo, error) {
	res, err := r.executor.Run(ctx, r.command, "inspect", "--type", "container", name)
	if err != nil {
		return nil, r.execErr(err, aerrors.CodeContainerInspectFailed, "docker.inspect")
	}
	if res.ExitCode != 0 {
		if isNotFound(res.Stderr) {
			return nil, aerrors.Newf(aerrors.CodeContainerNotFound, "container %q not found", name)
		}
		return nil, aerrors.Newf(aerrors.CodeContainerInspectFailed,
			"docker inspect %q failed: %s", name, truncate(res.Stderr, maxStderrLog))
	}

	info, err := parseInspectOutput(res.Stdout)
	if err != nil {
		return nil, err
	}
	return info, nil
}

// ListContainers returns containers matching the filter.
// When filter.ManagedOnly is true, only containers with devex.managed=true are returned.
func (r *CLIRuntime) ListContainers(ctx context.Context, filter ContainerFilter) ([]ContainerInfo, error) {
	args := []string{"ps", "-a", "--format", "{{json .}}"}

	if filter.ManagedOnly {
		args = append(args, "--filter", labelManaged+"="+labelManagedValue)
	}
	for k, v := range filter.Labels {
		args = append(args, "--filter", fmt.Sprintf("label=%s=%s", k, v))
	}

	res, err := r.executor.Run(ctx, r.command, args...)
	if err != nil {
		return nil, r.execErr(err, aerrors.CodeContainerListFailed, "docker.ps")
	}
	if res.ExitCode != 0 {
		return nil, aerrors.Newf(aerrors.CodeContainerListFailed,
			"docker ps failed: %s", truncate(res.Stderr, maxStderrLog))
	}

	containers, err := parsePSOutput(res.Stdout)
	if err != nil {
		return nil, err
	}

	// Apply client-side name prefix filter.
	if filter.NamePrefix != "" {
		filtered := containers[:0]
		for _, c := range containers {
			if strings.HasPrefix(c.Name, filter.NamePrefix) {
				filtered = append(filtered, c)
			}
		}
		containers = filtered
	}

	return containers, nil
}

// --- argument builders ---

func (r *CLIRuntime) buildRunArgs(spec ContainerSpec) []string {
	policy := spec.RestartPolicy
	if policy == "" {
		policy = "unless-stopped"
	}

	args := []string{"run", "-d", "--name", spec.Name, "--restart", policy}

	if spec.HostPort > 0 && spec.ContainerPort > 0 {
		args = append(args, "-p", fmt.Sprintf("%d:%d", spec.HostPort, spec.ContainerPort))
	}

	for k, v := range spec.Labels {
		args = append(args, "--label", k+"="+v)
	}

	if spec.Network != "" {
		args = append(args, "--network", spec.Network)
	}

	// Env vars are added to the command but must never appear in logs.
	for k, v := range spec.Env {
		args = append(args, "-e", k+"="+v)
	}

	args = append(args, spec.Image)
	return args
}

// --- output parsers ---

// dockerInspectEntry mirrors the relevant fields from `docker inspect` JSON.
type dockerInspectEntry struct {
	ID      string `json:"Id"`
	Name    string `json:"Name"`
	Created string `json:"Created"`
	Config  struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status   string `json:"Status"`
		Running  bool   `json:"Running"`
		ExitCode int    `json:"ExitCode"`
	} `json:"State"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

func parseInspectOutput(stdout string) (*ContainerInfo, error) {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" || stdout == "[]" {
		return nil, aerrors.New(aerrors.CodeContainerNotFound, "container not found (empty inspect output)")
	}

	var entries []dockerInspectEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		return nil, aerrors.Newf(aerrors.CodeContainerInspectFailed,
			"parse docker inspect output: %s", err)
	}
	if len(entries) == 0 {
		return nil, aerrors.New(aerrors.CodeContainerNotFound, "container not found (empty inspect array)")
	}

	e := &entries[0]
	info := &ContainerInfo{
		ID:      e.ID,
		Name:    strings.TrimPrefix(e.Name, "/"),
		Image:   e.Config.Image,
		Status:  e.State.Status,
		Running: e.State.Running,
		ExitCode: e.State.ExitCode,
		Labels:  e.Config.Labels,
	}

	if t, err := time.Parse(time.RFC3339Nano, e.Created); err == nil {
		info.CreatedAt = t
	}

	// Extract first host→container port binding.
	for portProto, bindings := range e.NetworkSettings.Ports {
		if len(bindings) == 0 {
			continue
		}
		containerPort, _, _ := strings.Cut(portProto, "/")
		cp, _ := strconv.Atoi(containerPort)
		hp, _ := strconv.Atoi(bindings[0].HostPort)
		if cp > 0 && hp > 0 {
			info.ContainerPort = cp
			info.HostPort = hp
			break
		}
	}

	return info, nil
}

// dockerPSEntry mirrors the fields from `docker ps --format '{{json .}}'`.
type dockerPSEntry struct {
	ID        string `json:"ID"`
	Names     string `json:"Names"`
	Image     string `json:"Image"`
	State     string `json:"State"`
	Status    string `json:"Status"`
	Labels    string `json:"Labels"`
	Ports     string `json:"Ports"`
	CreatedAt string `json:"CreatedAt"`
}

func parsePSOutput(stdout string) ([]ContainerInfo, error) {
	var containers []ContainerInfo
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry dockerPSEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, aerrors.Newf(aerrors.CodeContainerListFailed,
				"parse docker ps line: %s", err)
		}
		containers = append(containers, ContainerInfo{
			ID:      entry.ID,
			Name:    entry.Names,
			Image:   entry.Image,
			Status:  entry.Status,
			Running: strings.EqualFold(entry.State, "running"),
			Labels:  parseLabelsString(entry.Labels),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, aerrors.Newf(aerrors.CodeContainerListFailed,
			"scan docker ps output: %s", err)
	}
	return containers, nil
}

// parseLabelsString converts "k1=v1,k2=v2" to map[string]string.
func parseLabelsString(s string) map[string]string {
	if s == "" {
		return nil
	}
	m := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		k, v, _ := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)
		if k != "" {
			m[k] = v
		}
	}
	return m
}

// --- error helpers ---

func (r *CLIRuntime) execErr(err error, fallback aerrors.ErrorCode, op string) error {
	if aerrors.CodeOf(err) != "" {
		return err // already typed
	}
	return aerrors.WrapRetryable(fallback, op, err)
}

func (r *CLIRuntime) classifyPullError(stderr, image string) error {
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "not found") ||
		strings.Contains(low, "manifest unknown") ||
		strings.Contains(low, "no such image"):
		return aerrors.Newf(aerrors.CodeImageNotFound,
			"image %q not found in registry", image)
	default:
		return aerrors.Newf(aerrors.CodeImagePullFailed,
			"failed to pull %q: %s", image, truncate(stderr, maxStderrLog))
	}
}

func (r *CLIRuntime) classifyRunError(stderr, name string) error {
	if strings.Contains(strings.ToLower(stderr), "already in use") {
		return aerrors.Newf(aerrors.CodeContainerAlreadyExists,
			"container name %q already in use", name)
	}
	return aerrors.Newf(aerrors.CodeContainerStartFailed,
		"docker run %q failed: %s", name, truncate(stderr, maxStderrLog))
}

func isNotFound(stderr string) bool {
	low := strings.ToLower(stderr)
	return strings.Contains(low, "no such container") ||
		strings.Contains(low, "no such object") ||
		strings.Contains(low, "not found")
}

// --- validation ---

func validateContainerName(name string) error {
	if name == "" {
		return aerrors.New(aerrors.CodeContainerStartFailed, "container name must not be empty")
	}
	for _, ch := range name {
		if !isAllowedContainerChar(ch) {
			return aerrors.Newf(aerrors.CodeContainerStartFailed,
				"container name %q contains invalid character %q", name, ch)
		}
	}
	return nil
}

func isAllowedContainerChar(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') ||
		(ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9') ||
		ch == '-' || ch == '_' || ch == '.'
}

func validateImageName(image string) error {
	if image == "" {
		return aerrors.New(aerrors.CodeImagePullFailed, "image name must not be empty")
	}
	// Reject obvious shell injection patterns.
	for _, bad := range []string{";", "`", "$", "|", "&", ">", "<", "\n", "\r"} {
		if strings.Contains(image, bad) {
			return aerrors.Newf(aerrors.CodeImagePullFailed,
				"image name %q contains invalid character", image)
		}
	}
	return nil
}

// --- utility ---

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
