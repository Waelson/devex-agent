package docker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	aerrors "devex-agent/internal/errors"
)

// --- helpers ---

func newRuntime(t *testing.T) (*CLIRuntime, *FakeExecutor) {
	t.Helper()
	fake := &FakeExecutor{}
	rt := NewCLIRuntime("docker", fake, newNopLogger())
	return rt, fake
}

func assertErrorCode(t *testing.T, err error, want aerrors.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %q, got nil", want)
	}
	got := aerrors.CodeOf(err)
	if got != want {
		t.Errorf("error code: got %q, want %q (err: %v)", got, want, err)
	}
}

// argsContain returns true if all tokens appear (in any order) inside args.
func argsContain(args []string, tokens ...string) bool {
	for _, tok := range tokens {
		found := false
		for _, a := range args {
			if a == tok {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// argsContainPair returns true if the slice contains flag followed by value as adjacent elements.
func argsContainPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

// buildInspectJSON creates a minimal valid docker inspect JSON array.
func buildInspectJSON(t *testing.T, opts ...func(*dockerInspectEntry)) string {
	t.Helper()
	e := dockerInspectEntry{
		ID:      "abc123def456abc123def456abc123def456abc123def456abc123def456abc1",
		Name:    "/billing-api-dev-v42",
		Created: "2026-06-05T18:00:00.000000000Z",
	}
	e.Config.Image = "ghcr.io/useclarus/billing-api:v42"
	e.Config.Labels = map[string]string{
		"devex.managed":     "true",
		"devex.application": "billing-api",
	}
	e.State.Status = "running"
	e.State.Running = true
	e.State.ExitCode = 0
	e.NetworkSettings.Ports = map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}{
		"3000/tcp": {{HostIP: "0.0.0.0", HostPort: "4102"}},
	}
	for _, opt := range opts {
		opt(&e)
	}
	data, err := json.Marshal([]dockerInspectEntry{e})
	if err != nil {
		t.Fatalf("buildInspectJSON: %v", err)
	}
	return string(data)
}

// buildPSLine creates a single docker ps JSON line.
func buildPSLine(name, image, state string, labels map[string]string) string {
	labelParts := make([]string, 0, len(labels))
	for k, v := range labels {
		labelParts = append(labelParts, k+"="+v)
	}
	entry := dockerPSEntry{
		ID:    "abc123",
		Names: name,
		Image: image,
		State: state,
		Status: func() string {
			if state == "running" {
				return "Up 2 minutes"
			}
			return "Exited (0) 1 minute ago"
		}(),
		Labels: strings.Join(labelParts, ","),
	}
	data, _ := json.Marshal(entry)
	return string(data)
}

// newNopLogger returns a logger that discards output (keeps test output clean).
func newNopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// --- PullImage ---

func TestPullImage_Success(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{Stdout: "v42: Pulling from useclarus/billing-api\nStatus: Downloaded newer image"})

	if err := rt.PullImage(context.Background(), "ghcr.io/useclarus/billing-api:v42"); err != nil {
		t.Fatalf("PullImage: %v", err)
	}

	call := fake.LastCall()
	if call.Name != "docker" {
		t.Errorf("binary: got %q, want docker", call.Name)
	}
	if !argsContain(call.Args, "pull", "ghcr.io/useclarus/billing-api:v42") {
		t.Errorf("args: %v", call.Args)
	}
}

func TestPullImage_ImageNotFound(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{
		ExitCode: 1,
		Stderr:   "Error response from daemon: manifest unknown: manifest not found",
	})

	err := rt.PullImage(context.Background(), "ghcr.io/no-such/image:v99")
	assertErrorCode(t, err, aerrors.CodeImageNotFound)
}

func TestPullImage_GeneralFailure(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{
		ExitCode: 1,
		Stderr:   "Error: temporary network failure",
	})

	err := rt.PullImage(context.Background(), "ghcr.io/useclarus/billing-api:v42")
	assertErrorCode(t, err, aerrors.CodeImagePullFailed)
}

func TestPullImage_EmptyImageName(t *testing.T) {
	rt, _ := newRuntime(t)
	err := rt.PullImage(context.Background(), "")
	assertErrorCode(t, err, aerrors.CodeImagePullFailed)
}

func TestPullImage_InvalidImageName(t *testing.T) {
	rt, _ := newRuntime(t)
	err := rt.PullImage(context.Background(), "image;rm -rf /")
	assertErrorCode(t, err, aerrors.CodeImagePullFailed)
}

// --- StartContainer ---

func TestStartContainer_Success(t *testing.T) {
	rt, fake := newRuntime(t)
	containerID := "abc123def456"
	fake.Enqueue(CommandResult{Stdout: containerID + "\n"})

	info, err := rt.StartContainer(context.Background(), ContainerSpec{
		Name:          "billing-api-dev-v42",
		Image:         "ghcr.io/useclarus/billing-api:v42",
		HostPort:      4102,
		ContainerPort: 3000,
		Labels:        map[string]string{"devex.managed": "true"},
		RestartPolicy: "unless-stopped",
	})
	if err != nil {
		t.Fatalf("StartContainer: %v", err)
	}
	if !strings.HasPrefix(info.ID, containerID) {
		t.Errorf("ID: got %q", info.ID)
	}
	if info.Name != "billing-api-dev-v42" {
		t.Errorf("Name: got %q", info.Name)
	}
	if info.HostPort != 4102 {
		t.Errorf("HostPort: got %d", info.HostPort)
	}
	if !info.Running {
		t.Error("Running must be true after start")
	}
}

func TestStartContainer_BuildsRunFlag(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{Stdout: "abc123\n"})

	_, _ = rt.StartContainer(context.Background(), ContainerSpec{
		Name:          "billing-api-dev-v42",
		Image:         "ghcr.io/useclarus/billing-api:v42",
		HostPort:      4102,
		ContainerPort: 3000,
	})

	call := fake.LastCall()
	// Must use -d (detach)
	if !argsContain(call.Args, "run", "-d") {
		t.Errorf("missing 'run -d': args=%v", call.Args)
	}
	// Must set container name
	if !argsContainPair(call.Args, "--name", "billing-api-dev-v42") {
		t.Errorf("missing --name: args=%v", call.Args)
	}
	// Must publish port
	if !argsContainPair(call.Args, "-p", "4102:3000") {
		t.Errorf("missing -p 4102:3000: args=%v", call.Args)
	}
	// Image must be the last arg
	if call.Args[len(call.Args)-1] != "ghcr.io/useclarus/billing-api:v42" {
		t.Errorf("image must be last arg: args=%v", call.Args)
	}
}

func TestStartContainer_AppliesLabels(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{Stdout: "abc123\n"})

	_, _ = rt.StartContainer(context.Background(), ContainerSpec{
		Name:  "billing-api-dev-v42",
		Image: "ghcr.io/billing:v42",
		Labels: map[string]string{
			"devex.managed":     "true",
			"devex.application": "billing-api",
		},
	})

	call := fake.LastCall()
	if !argsContainPair(call.Args, "--label", "devex.managed=true") {
		t.Errorf("devex.managed label missing: args=%v", call.Args)
	}
}

func TestStartContainer_AppliesRestartPolicy(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{Stdout: "abc123\n"})

	_, _ = rt.StartContainer(context.Background(), ContainerSpec{
		Name:          "billing-api-dev-v42",
		Image:         "ghcr.io/billing:v42",
		RestartPolicy: "unless-stopped",
	})

	call := fake.LastCall()
	if !argsContainPair(call.Args, "--restart", "unless-stopped") {
		t.Errorf("restart policy missing: args=%v", call.Args)
	}
}

func TestStartContainer_DefaultRestartPolicy(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{Stdout: "abc123\n"})

	_, _ = rt.StartContainer(context.Background(), ContainerSpec{
		Name:  "billing-api-dev-v42",
		Image: "ghcr.io/billing:v42",
		// RestartPolicy intentionally empty
	})

	call := fake.LastCall()
	if !argsContainPair(call.Args, "--restart", "unless-stopped") {
		t.Errorf("default restart policy not applied: args=%v", call.Args)
	}
}

func TestStartContainer_EnvVarsAddedToArgs(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{Stdout: "abc123\n"})

	_, _ = rt.StartContainer(context.Background(), ContainerSpec{
		Name:  "billing-api-dev-v42",
		Image: "ghcr.io/billing:v42",
		Env:   map[string]string{"NODE_ENV": "development"},
	})

	call := fake.LastCall()
	if !argsContainPair(call.Args, "-e", "NODE_ENV=development") {
		t.Errorf("env var missing from args: args=%v", call.Args)
	}
}

func TestStartContainer_AlreadyExists(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{
		ExitCode: 1,
		Stderr:   `docker: Error response from daemon: Conflict. The container name "/billing-api-dev-v42" is already in use`,
	})

	_, err := rt.StartContainer(context.Background(), ContainerSpec{
		Name:  "billing-api-dev-v42",
		Image: "ghcr.io/billing:v42",
	})
	assertErrorCode(t, err, aerrors.CodeContainerAlreadyExists)
}

func TestStartContainer_GeneralFailure(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{
		ExitCode: 1,
		Stderr:   "docker: Error response from daemon: OCI runtime create failed",
	})

	_, err := rt.StartContainer(context.Background(), ContainerSpec{
		Name:  "billing-api-dev-v42",
		Image: "ghcr.io/billing:v42",
	})
	assertErrorCode(t, err, aerrors.CodeContainerStartFailed)
}

func TestStartContainer_InvalidContainerName(t *testing.T) {
	rt, _ := newRuntime(t)
	_, err := rt.StartContainer(context.Background(), ContainerSpec{
		Name:  "bad name with spaces",
		Image: "ghcr.io/billing:v42",
	})
	assertErrorCode(t, err, aerrors.CodeContainerStartFailed)
}

// --- StopContainer ---

func TestStopContainer_Success(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{Stdout: "billing-api-dev-v42\n"})

	if err := rt.StopContainer(context.Background(), "billing-api-dev-v42", 30*time.Second); err != nil {
		t.Fatalf("StopContainer: %v", err)
	}

	call := fake.LastCall()
	if !argsContain(call.Args, "stop") {
		t.Errorf("missing 'stop': args=%v", call.Args)
	}
	if !argsContainPair(call.Args, "--time", "30") {
		t.Errorf("missing --time 30: args=%v", call.Args)
	}
	if call.Args[len(call.Args)-1] != "billing-api-dev-v42" {
		t.Errorf("container name must be last arg: args=%v", call.Args)
	}
}

func TestStopContainer_NotFound(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{
		ExitCode: 1,
		Stderr:   "Error response from daemon: No such container: billing-api-dev-v42",
	})

	err := rt.StopContainer(context.Background(), "billing-api-dev-v42", 10*time.Second)
	assertErrorCode(t, err, aerrors.CodeContainerNotFound)
}

func TestStopContainer_GeneralFailure(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{
		ExitCode: 1,
		Stderr:   "Error: cannot stop container: permission denied",
	})

	err := rt.StopContainer(context.Background(), "billing-api-dev-v42", 10*time.Second)
	assertErrorCode(t, err, aerrors.CodeContainerStopFailed)
}

// --- RemoveContainer ---

func TestRemoveContainer_Success(t *testing.T) {
	rt, fake := newRuntime(t)
	// First call: inspect → managed container
	fake.Enqueue(CommandResult{Stdout: buildInspectJSON(t)})
	// Second call: docker rm
	fake.Enqueue(CommandResult{Stdout: "billing-api-dev-v42\n"})

	if err := rt.RemoveContainer(context.Background(), "billing-api-dev-v42"); err != nil {
		t.Fatalf("RemoveContainer: %v", err)
	}

	if fake.CallCount() != 2 {
		t.Errorf("expected 2 calls (inspect + rm), got %d", fake.CallCount())
	}
	rmCall := fake.CallAt(1)
	if !argsContain(rmCall.Args, "rm") {
		t.Errorf("second call must be 'rm': args=%v", rmCall.Args)
	}
}

func TestRemoveContainer_NotManaged(t *testing.T) {
	rt, fake := newRuntime(t)
	// Inspect returns container WITHOUT devex.managed=true
	fake.Enqueue(CommandResult{
		Stdout: buildInspectJSON(t, func(e *dockerInspectEntry) {
			e.Config.Labels = map[string]string{"app": "custom"} // no devex.managed
		}),
	})

	err := rt.RemoveContainer(context.Background(), "some-container")
	assertErrorCode(t, err, aerrors.CodeContainerNotManaged)

	// rm must NOT be called
	if fake.CallCount() != 1 {
		t.Errorf("rm must not be called for unmanaged container; calls=%d", fake.CallCount())
	}
}

func TestRemoveContainer_ContainerNotFound(t *testing.T) {
	rt, fake := newRuntime(t)
	// Inspect returns not-found
	fake.Enqueue(CommandResult{
		ExitCode: 1,
		Stderr:   "Error: No such object: billing-api-dev-v42",
	})

	err := rt.RemoveContainer(context.Background(), "billing-api-dev-v42")
	assertErrorCode(t, err, aerrors.CodeContainerNotFound)
}

// --- InspectContainer ---

func TestInspectContainer_Success(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{Stdout: buildInspectJSON(t)})

	info, err := rt.InspectContainer(context.Background(), "billing-api-dev-v42")
	if err != nil {
		t.Fatalf("InspectContainer: %v", err)
	}

	if info.Name != "billing-api-dev-v42" {
		t.Errorf("Name: got %q", info.Name)
	}
	if info.Image != "ghcr.io/useclarus/billing-api:v42" {
		t.Errorf("Image: got %q", info.Image)
	}
	if !info.Running {
		t.Error("Running should be true")
	}
	if info.HostPort != 4102 {
		t.Errorf("HostPort: got %d", info.HostPort)
	}
	if info.ContainerPort != 3000 {
		t.Errorf("ContainerPort: got %d", info.ContainerPort)
	}
	if info.Labels["devex.managed"] != "true" {
		t.Errorf("devex.managed label: got %q", info.Labels["devex.managed"])
	}
	if info.CreatedAt.IsZero() {
		t.Error("CreatedAt must not be zero")
	}
}

func TestInspectContainer_NotFound(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{
		ExitCode: 1,
		Stderr:   "Error: No such object: billing-api-dev-v42",
	})

	_, err := rt.InspectContainer(context.Background(), "billing-api-dev-v42")
	assertErrorCode(t, err, aerrors.CodeContainerNotFound)
}

func TestInspectContainer_EmptyOutput(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{Stdout: "[]"})

	_, err := rt.InspectContainer(context.Background(), "billing-api-dev-v42")
	assertErrorCode(t, err, aerrors.CodeContainerNotFound)
}

func TestInspectContainer_MalformedJSON(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{Stdout: "{bad json"})

	_, err := rt.InspectContainer(context.Background(), "billing-api-dev-v42")
	assertErrorCode(t, err, aerrors.CodeContainerInspectFailed)
}

func TestInspectContainer_StoppedContainer(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{
		Stdout: buildInspectJSON(t, func(e *dockerInspectEntry) {
			e.State.Running = false
			e.State.Status = "exited"
			e.State.ExitCode = 1
		}),
	})

	info, err := rt.InspectContainer(context.Background(), "billing-api-dev-v42")
	if err != nil {
		t.Fatalf("InspectContainer: %v", err)
	}
	if info.Running {
		t.Error("Running should be false")
	}
	if info.ExitCode != 1 {
		t.Errorf("ExitCode: got %d", info.ExitCode)
	}
}

// --- ListContainers ---

func TestListContainers_Success(t *testing.T) {
	rt, fake := newRuntime(t)
	line1 := buildPSLine("billing-api-dev-v42", "ghcr.io/billing:v42", "running",
		map[string]string{"devex.managed": "true", "devex.application": "billing-api"})
	line2 := buildPSLine("orders-api-dev-v10", "ghcr.io/orders:v10", "running",
		map[string]string{"devex.managed": "true", "devex.application": "orders-api"})
	fake.Enqueue(CommandResult{Stdout: line1 + "\n" + line2 + "\n"})

	containers, err := rt.ListContainers(context.Background(), ContainerFilter{ManagedOnly: true})
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(containers))
	}
	if containers[0].Name != "billing-api-dev-v42" {
		t.Errorf("first container name: got %q", containers[0].Name)
	}
	if !containers[0].Running {
		t.Error("first container should be running")
	}
}

func TestListContainers_Empty(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{Stdout: ""})

	containers, err := rt.ListContainers(context.Background(), ContainerFilter{ManagedOnly: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(containers) != 0 {
		t.Errorf("expected 0 containers, got %d", len(containers))
	}
}

func TestListContainers_ManagedOnlyFilter(t *testing.T) {
	rt, fake := newRuntime(t)
	fake.Enqueue(CommandResult{Stdout: ""})

	_, _ = rt.ListContainers(context.Background(), ContainerFilter{ManagedOnly: true})

	call := fake.LastCall()
	// Must pass label filter to docker
	if !argsContainPair(call.Args, "--filter", "label=devex.managed=true") {
		t.Errorf("managed-only filter not applied: args=%v", call.Args)
	}
}

func TestListContainers_NamePrefixFilter(t *testing.T) {
	rt, fake := newRuntime(t)
	line1 := buildPSLine("billing-api-dev-v42", "ghcr.io/billing:v42", "running", nil)
	line2 := buildPSLine("orders-api-dev-v10", "ghcr.io/orders:v10", "running", nil)
	fake.Enqueue(CommandResult{Stdout: line1 + "\n" + line2 + "\n"})

	containers, err := rt.ListContainers(context.Background(), ContainerFilter{
		NamePrefix: "billing-",
	})
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	if len(containers) != 1 {
		t.Fatalf("expected 1 container with prefix 'billing-', got %d", len(containers))
	}
	if containers[0].Name != "billing-api-dev-v42" {
		t.Errorf("name: got %q", containers[0].Name)
	}
}

func TestListContainers_ParsesManagedLabels(t *testing.T) {
	rt, fake := newRuntime(t)
	line := buildPSLine("billing-api-dev-v42", "ghcr.io/billing:v42", "running",
		map[string]string{"devex.managed": "true", "devex.deployment_id": "dep_456"})
	fake.Enqueue(CommandResult{Stdout: line + "\n"})

	containers, err := rt.ListContainers(context.Background(), ContainerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if containers[0].Labels["devex.managed"] != "true" {
		t.Errorf("devex.managed: got %q", containers[0].Labels["devex.managed"])
	}
	if containers[0].Labels["devex.deployment_id"] != "dep_456" {
		t.Errorf("devex.deployment_id: got %q", containers[0].Labels["devex.deployment_id"])
	}
}

// --- FakeExecutor ---

func TestFakeExecutor_RecordsCalls(t *testing.T) {
	fake := &FakeExecutor{}
	_, _ = fake.Run(context.Background(), "docker", "pull", "image:v1")
	_, _ = fake.Run(context.Background(), "docker", "run", "-d", "image:v1")

	if fake.CallCount() != 2 {
		t.Errorf("expected 2 calls, got %d", fake.CallCount())
	}
	if fake.CallAt(0).Args[0] != "pull" {
		t.Errorf("first call should be pull: %v", fake.CallAt(0).Args)
	}
	if fake.CallAt(1).Args[0] != "run" {
		t.Errorf("second call should be run: %v", fake.CallAt(1).Args)
	}
}

func TestFakeExecutor_ConsumesQueuedResponses(t *testing.T) {
	fake := &FakeExecutor{}
	fake.Enqueue(
		CommandResult{Stdout: "first"},
		CommandResult{Stdout: "second"},
	)

	r1, _ := fake.Run(context.Background(), "docker", "pull", "img:1")
	r2, _ := fake.Run(context.Background(), "docker", "pull", "img:2")
	r3, _ := fake.Run(context.Background(), "docker", "pull", "img:3") // queue empty → default

	if r1.Stdout != "first" {
		t.Errorf("r1: got %q", r1.Stdout)
	}
	if r2.Stdout != "second" {
		t.Errorf("r2: got %q", r2.Stdout)
	}
	if r3.ExitCode != 0 || r3.Stdout != "" {
		t.Errorf("r3 (default): got %+v", r3)
	}
}

func TestFakeExecutor_Reset(t *testing.T) {
	fake := &FakeExecutor{}
	fake.Enqueue(CommandResult{Stdout: "data"})
	_, _ = fake.Run(context.Background(), "docker", "ps")

	fake.Reset()

	if fake.CallCount() != 0 {
		t.Errorf("expected 0 calls after reset, got %d", fake.CallCount())
	}
	r, _ := fake.Run(context.Background(), "docker", "ps")
	if r.Stdout != "" {
		t.Errorf("expected empty response after reset, got %q", r.Stdout)
	}
}

// --- utility helpers ---

func TestParseLabelsString(t *testing.T) {
	tests := []struct {
		input string
		want  map[string]string
	}{
		{"", nil},
		{"devex.managed=true", map[string]string{"devex.managed": "true"}},
		{"k1=v1,k2=v2", map[string]string{"k1": "v1", "k2": "v2"}},
		{"flag", map[string]string{"flag": ""}},
	}
	for _, tc := range tests {
		got := parseLabelsString(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("input=%q: len got %d, want %d", tc.input, len(got), len(tc.want))
			continue
		}
		for k, v := range tc.want {
			if got[k] != v {
				t.Errorf("input=%q key=%q: got %q, want %q", tc.input, k, got[k], v)
			}
		}
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	result := truncate("hello world", 5)
	if !strings.HasPrefix(result, "hello") {
		t.Errorf("truncated: got %q", result)
	}
	if len([]rune(result)) <= 5 {
		// truncate appends an ellipsis, so byte length may be >5
	}
}

func TestValidateContainerName(t *testing.T) {
	valid := []string{"billing-api-dev-v42", "app_v1", "my.container"}
	for _, name := range valid {
		if err := validateContainerName(name); err != nil {
			t.Errorf("valid name %q rejected: %v", name, err)
		}
	}
	invalid := []string{"", "bad name", "bad;name", "bad/name"}
	for _, name := range invalid {
		if err := validateContainerName(name); err == nil {
			t.Errorf("invalid name %q should be rejected", name)
		}
	}
}

func TestValidateImageName(t *testing.T) {
	valid := []string{
		"ghcr.io/useclarus/billing-api:v42",
		"123456789012.dkr.ecr.sa-east-1.amazonaws.com/billing-api:v42",
		"billing-api:latest",
	}
	for _, img := range valid {
		if err := validateImageName(img); err != nil {
			t.Errorf("valid image %q rejected: %v", img, err)
		}
	}
	invalid := []string{"", "image;rm -rf /", "img`id`", "img|cat /etc/passwd"}
	for _, img := range invalid {
		if err := validateImageName(img); err == nil {
			t.Errorf("invalid image %q should be rejected", img)
		}
	}
}
