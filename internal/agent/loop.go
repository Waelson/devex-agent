package agent

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"sync"
	"time"

	"devex-agent/internal/config"
	"devex-agent/internal/docker"
	"devex-agent/internal/health"
	"devex-agent/internal/platform"
	"devex-agent/internal/ports"
	"devex-agent/internal/state"
)

// AgentVersion is the current agent binary version, reported on registration and heartbeat.
const AgentVersion = "0.1.0"

// --- dependency interfaces ---

// PlatformClient is the interface for communicating with the DevEx Platform API.
type PlatformClient interface {
	SetAgentID(id string)
	Register(ctx context.Context, req platform.RegisterRequest) (*platform.RegisterResponse, error)
	SendHeartbeat(ctx context.Context, req platform.HeartbeatRequest) (*platform.HeartbeatResponse, error)
	FetchPendingCommands(ctx context.Context) ([]platform.PendingCommand, error)
	ClaimCommand(ctx context.Context, commandID string) (*platform.ClaimResponse, error)
	StartCommand(ctx context.Context, commandID string) (*platform.StartResponse, error)
	ReportCommand(ctx context.Context, commandID string, req platform.CommandReportRequest) error
}

// DockerRuntime wraps Docker container lifecycle operations.
type DockerRuntime interface {
	PullImage(ctx context.Context, image string) error
	StartContainer(ctx context.Context, spec docker.ContainerSpec) (*docker.ContainerInfo, error)
	StopContainer(ctx context.Context, name string, timeout time.Duration) error
	RemoveContainer(ctx context.Context, name string) error
	InspectContainer(ctx context.Context, name string) (*docker.ContainerInfo, error)
	ListContainers(ctx context.Context, filter docker.ContainerFilter) ([]docker.ContainerInfo, error)
}

// PortManager wraps host-port allocation operations.
type PortManager interface {
	Allocate(ctx context.Context, spec ports.AllocateSpec) (int, error)
	MarkActive(port int) error
	MarkDraining(port int) error
	Release(port int) error
	CountActive() (int, error)
	Reconcile(ctx context.Context, liveContainerNames map[string]bool) ([]ports.ReconcileEvent, error)
	Snapshot() (*ports.PortState, error)
}

// StateStore wraps local state persistence.
type StateStore interface {
	LoadAgentIdentity() (*state.AgentIdentity, error)
	SaveAgentIdentity(id *state.AgentIdentity) error
	LoadLocalState() (*state.LocalState, error)
	SaveLocalState(s *state.LocalState) error
}

// HealthChecker wraps health check operations.
type HealthChecker interface {
	CheckHTTP(ctx context.Context, target health.HTTPCheckTarget) (*health.Result, error)
	CheckContainer(ctx context.Context, status health.ContainerStatus) (*health.Result, error)
}

// Dependencies holds all external dependencies injected into the RuntimeAgent.
type Dependencies struct {
	Platform PlatformClient
	Docker   DockerRuntime
	Ports    PortManager
	State    StateStore
	Health   HealthChecker
}

// RuntimeAgent is the main agent orchestrator for runtime (workload-execution) instances.
type RuntimeAgent struct {
	cfg        *config.Config
	deps       Dependencies
	logger     *slog.Logger
	privateIP  string
	hostname   string
	instanceID string

	mu         sync.Mutex        // protects localState
	localState *state.LocalState
	agentID    string
}

// New creates a RuntimeAgent with the given configuration and dependencies.
func New(cfg *config.Config, deps Dependencies, logger *slog.Logger) *RuntimeAgent {
	return &RuntimeAgent{
		cfg:    cfg,
		deps:   deps,
		logger: logger,
	}
}

// Run is the main entry point. It blocks until ctx is cancelled.
// Boot sequence: discover host info → register → load state → reconcile → start loops.
func (a *RuntimeAgent) Run(ctx context.Context) error {
	a.hostname = discoverHostname()
	a.instanceID = discoverInstanceID()
	a.privateIP = discoverPrivateIP()

	a.logger.Info("runtime-agent starting",
		"version", AgentVersion,
		"hostname", a.hostname,
		"private_ip", a.privateIP,
		"environment", a.cfg.Agent.Environment,
		"role", a.cfg.Agent.Role,
	)

	if err := a.ensureRegistered(ctx); err != nil {
		return fmt.Errorf("agent registration: %w", err)
	}

	if err := a.loadLocalState(); err != nil {
		return fmt.Errorf("load local state: %w", err)
	}

	containers, listErr := a.deps.Docker.ListContainers(ctx, docker.ContainerFilter{ManagedOnly: true})
	if listErr != nil {
		a.logger.Warn("startup container list failed; skipping reconciliation", "error", listErr)
	} else {
		if err := a.reconcilePorts(ctx, containers); err != nil {
			a.logger.Warn("startup port reconciliation failed; continuing", "error", err)
		}
		a.reconcileDeployments(ctx, containers)
	}

	go a.heartbeatLoop(ctx)
	go a.drainCleanupLoop(ctx)
	a.commandPollLoop(ctx)
	return nil
}

// loadLocalState reads or creates the local state from storage.
func (a *RuntimeAgent) loadLocalState() error {
	ls, err := a.deps.State.LoadLocalState()
	if err != nil {
		return err
	}
	if ls == nil {
		ls = &state.LocalState{
			AgentID:     a.agentID,
			Mode:        a.cfg.Agent.Mode,
			Environment: a.cfg.Agent.Environment,
			Role:        a.cfg.Agent.Role,
		}
	}
	a.mu.Lock()
	a.localState = ls
	a.mu.Unlock()
	return nil
}

// modifyState acquires the mutex, applies fn to the local state, then persists it.
// All mutations to localState must go through this function.
func (a *RuntimeAgent) modifyState(fn func(*state.LocalState)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.localState == nil {
		a.localState = &state.LocalState{
			AgentID:     a.agentID,
			Mode:        a.cfg.Agent.Mode,
			Environment: a.cfg.Agent.Environment,
		}
	}
	fn(a.localState)
	return a.deps.State.SaveLocalState(a.localState)
}

// reconcilePorts aligns the port state file with live Docker containers.
func (a *RuntimeAgent) reconcilePorts(ctx context.Context, containers []docker.ContainerInfo) error {
	live := make(map[string]bool, len(containers))
	for _, c := range containers {
		live[c.Name] = true
	}
	events, err := a.deps.Ports.Reconcile(ctx, live)
	if err != nil {
		return err
	}
	for _, ev := range events {
		a.logger.Info("port reconciled",
			"port", ev.Port,
			"old_status", ev.OldStatus,
			"reason", ev.Reason,
		)
	}
	return nil
}

// reconcileDeployments compares local state against live Docker containers and
// marks entries as inconsistent when their container has disappeared or stopped.
// Orphaned managed containers (present in Docker, absent from state) are logged as warnings.
func (a *RuntimeAgent) reconcileDeployments(ctx context.Context, containers []docker.ContainerInfo) {
	live := make(map[string]docker.ContainerInfo, len(containers))
	for _, c := range containers {
		live[c.Name] = c
	}

	now := time.Now().UTC()
	_ = a.modifyState(func(ls *state.LocalState) {
		for i := range ls.Deployments {
			entry := &ls.Deployments[i]
			if isTerminalDeploymentStatus(entry.Status) {
				continue
			}
			info, found := live[entry.ContainerName]
			if !found {
				// Container absent from Docker — only flag statuses where running is expected.
				if isRunningExpectedStatus(entry.Status) {
					a.logger.Warn("reconcile: managed container absent from Docker; marking inconsistent",
						"container_name", entry.ContainerName,
						"deployment_id", entry.DeploymentID,
						"status", string(entry.Status),
					)
					entry.Status = state.DeploymentStatusInconsistent
					entry.UpdatedAt = now
				}
				continue
			}
			// Container present but stopped while we expect it to be running.
			if !info.Running && entry.Status == state.DeploymentStatusActive {
				a.logger.Warn("reconcile: active container is not running; marking inconsistent",
					"container_name", entry.ContainerName,
					"deployment_id", entry.DeploymentID,
					"docker_status", info.Status,
				)
				entry.Status = state.DeploymentStatusInconsistent
				entry.UpdatedAt = now
			}
		}
	})

	// Log any managed containers in Docker that have no matching local state entry.
	a.mu.Lock()
	ls := a.localState
	a.mu.Unlock()
	known := make(map[string]bool)
	if ls != nil {
		for _, d := range ls.Deployments {
			known[d.ContainerName] = true
		}
	}
	for _, c := range containers {
		if !known[c.Name] {
			a.logger.Warn("reconcile: orphaned managed container found with no local state",
				"container_name", c.Name,
			)
		}
	}
}

// isTerminalDeploymentStatus returns true for states where no further
// Docker container changes are expected and reconciliation should skip the entry.
func isTerminalDeploymentStatus(s state.DeploymentStatus) bool {
	return s == state.DeploymentStatusRemoved || s == state.DeploymentStatusFailed
}

// isRunningExpectedStatus returns true for states where the container is expected
// to be present and running in Docker.
func isRunningExpectedStatus(s state.DeploymentStatus) bool {
	return s == state.DeploymentStatusActive ||
		s == state.DeploymentStatusCheckingHealth ||
		s == state.DeploymentStatusStarting
}

// commandPollLoop polls for and processes commands until ctx is cancelled.
func (a *RuntimeAgent) commandPollLoop(ctx context.Context) {
	interval := time.Duration(a.cfg.Runtime.CommandPollIntervalSecs) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}

	for {
		select {
		case <-ctx.Done():
			a.logger.Info("command poll loop stopping")
			return
		default:
		}

		a.pollAndProcessCommands(ctx)

		// Small jitter (up to 10% of interval) to avoid thundering herd.
		jitter := time.Duration(rand.Int64N(int64(interval/10) + 1))
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval + jitter):
		}
	}
}

// --- host-info helpers ---

func discoverPrivateIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip4 := ip.To4(); ip4 != nil && !ip4.IsLoopback() {
				return ip4.String()
			}
		}
	}
	return "127.0.0.1"
}

func discoverHostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return name
}

func discoverInstanceID() string {
	// In production EC2, this would read from instance metadata service.
	// For MVP, return empty string; the Platform accepts it.
	return ""
}
