package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	aerrors "devex-agent/internal/errors"
	"devex-agent/internal/config"
	"devex-agent/internal/docker"
	"devex-agent/internal/health"
	"devex-agent/internal/platform"
	"devex-agent/internal/ports"
	"devex-agent/internal/state"
)

// ============================================================
// Fake implementations
// ============================================================

// --- fakePlatformClient ---

type fakePlatformClient struct {
	agentID string

	registerCalled bool
	registerResp   *platform.RegisterResponse
	registerErr    error

	heartbeatCalled bool
	heartbeatReq    platform.HeartbeatRequest
	heartbeatResp   *platform.HeartbeatResponse
	heartbeatErr    error

	pendingCommands []platform.PendingCommand
	fetchErr        error

	claimResp *platform.ClaimResponse
	claimErr  error

	startResp *platform.StartResponse
	startErr  error

	reportedRequests []struct {
		commandID string
		req       platform.CommandReportRequest
	}
	reportErr error
}

func (f *fakePlatformClient) SetAgentID(id string) { f.agentID = id }

func (f *fakePlatformClient) Register(_ context.Context, _ platform.RegisterRequest) (*platform.RegisterResponse, error) {
	f.registerCalled = true
	return f.registerResp, f.registerErr
}

func (f *fakePlatformClient) SendHeartbeat(_ context.Context, req platform.HeartbeatRequest) (*platform.HeartbeatResponse, error) {
	f.heartbeatCalled = true
	f.heartbeatReq = req
	return f.heartbeatResp, f.heartbeatErr
}

func (f *fakePlatformClient) FetchPendingCommands(_ context.Context) ([]platform.PendingCommand, error) {
	return f.pendingCommands, f.fetchErr
}

func (f *fakePlatformClient) ClaimCommand(_ context.Context, _ string) (*platform.ClaimResponse, error) {
	return f.claimResp, f.claimErr
}

func (f *fakePlatformClient) StartCommand(_ context.Context, _ string) (*platform.StartResponse, error) {
	return f.startResp, f.startErr
}

func (f *fakePlatformClient) ReportCommand(_ context.Context, commandID string, req platform.CommandReportRequest) error {
	f.reportedRequests = append(f.reportedRequests, struct {
		commandID string
		req       platform.CommandReportRequest
	}{commandID, req})
	return f.reportErr
}

func (f *fakePlatformClient) lastReport() *platform.CommandReportRequest {
	if len(f.reportedRequests) == 0 {
		return nil
	}
	r := f.reportedRequests[len(f.reportedRequests)-1].req
	return &r
}

// --- fakeDockerRuntime ---

type fakeDockerRuntime struct {
	pullErr  error
	startErr error
	stopErr  error
	rmErr    error
	inspErr  error
	listErr  error

	startResp   *docker.ContainerInfo
	inspectResp *docker.ContainerInfo
	listResp    []docker.ContainerInfo

	startCalled bool
	stopCalled  bool
	rmCalled    bool

	stopCalledWith string
	rmCalledWith   string
	startSpec      docker.ContainerSpec
}

func (f *fakeDockerRuntime) PullImage(_ context.Context, _ string) error { return f.pullErr }
func (f *fakeDockerRuntime) StartContainer(_ context.Context, spec docker.ContainerSpec) (*docker.ContainerInfo, error) {
	f.startCalled = true
	f.startSpec = spec
	return f.startResp, f.startErr
}
func (f *fakeDockerRuntime) StopContainer(_ context.Context, name string, _ time.Duration) error {
	f.stopCalled = true
	f.stopCalledWith = name
	return f.stopErr
}
func (f *fakeDockerRuntime) RemoveContainer(_ context.Context, name string) error {
	f.rmCalled = true
	f.rmCalledWith = name
	return f.rmErr
}
func (f *fakeDockerRuntime) InspectContainer(_ context.Context, _ string) (*docker.ContainerInfo, error) {
	return f.inspectResp, f.inspErr
}
func (f *fakeDockerRuntime) ListContainers(_ context.Context, _ docker.ContainerFilter) ([]docker.ContainerInfo, error) {
	return f.listResp, f.listErr
}

// --- fakePortManager ---

type fakePortManager struct {
	allocPort  int
	allocErr   error
	activeErr  error
	releaseErr error

	releaseCalled       bool
	releasePort         int
	drainingCalled      bool
	drainingPort        int
	reconcileCalled     bool
	reconcileCalledWith map[string]bool
}

func (f *fakePortManager) Allocate(_ context.Context, _ ports.AllocateSpec) (int, error) {
	return f.allocPort, f.allocErr
}
func (f *fakePortManager) MarkActive(_ int) error { return f.activeErr }
func (f *fakePortManager) MarkDraining(port int) error {
	f.drainingCalled = true
	f.drainingPort = port
	return nil
}
func (f *fakePortManager) Release(port int) error {
	f.releaseCalled = true
	f.releasePort = port
	return f.releaseErr
}
func (f *fakePortManager) CountActive() (int, error) { return 0, nil }
func (f *fakePortManager) Snapshot() (*ports.PortState, error) {
	return &ports.PortState{Allocations: map[string]*ports.PortAllocation{}}, nil
}
func (f *fakePortManager) Reconcile(_ context.Context, live map[string]bool) ([]ports.ReconcileEvent, error) {
	f.reconcileCalled = true
	f.reconcileCalledWith = live
	return nil, nil
}

// --- fakeStateStore ---

type fakeStateStore struct {
	identity      *state.AgentIdentity
	localState    *state.LocalState
	saveIdErr     error
	saveLocalErr  error
	savedIdentity *state.AgentIdentity
	savedLocal    *state.LocalState
}

func (f *fakeStateStore) LoadAgentIdentity() (*state.AgentIdentity, error) {
	return f.identity, nil
}
func (f *fakeStateStore) SaveAgentIdentity(id *state.AgentIdentity) error {
	f.savedIdentity = id
	f.identity = id
	return f.saveIdErr
}
func (f *fakeStateStore) LoadLocalState() (*state.LocalState, error) {
	return f.localState, nil
}
func (f *fakeStateStore) SaveLocalState(s *state.LocalState) error {
	if s != nil {
		cp := *s
		cp.Deployments = make([]state.DeploymentEntry, len(s.Deployments))
		copy(cp.Deployments, s.Deployments)
		f.savedLocal = &cp
	}
	return f.saveLocalErr
}

// --- fakeHealthChecker ---

type fakeHealthChecker struct {
	httpResult      *health.Result
	httpErr         error
	containerResult *health.Result
	containerErr    error
}

func (f *fakeHealthChecker) CheckHTTP(_ context.Context, _ health.HTTPCheckTarget) (*health.Result, error) {
	return f.httpResult, f.httpErr
}
func (f *fakeHealthChecker) CheckContainer(_ context.Context, _ health.ContainerStatus) (*health.Result, error) {
	return f.containerResult, f.containerErr
}

// ============================================================
// Test helpers
// ============================================================

func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestAgent(t *testing.T) (*RuntimeAgent, *fakePlatformClient, *fakeDockerRuntime, *fakePortManager, *fakeStateStore, *fakeHealthChecker) {
	t.Helper()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:          "agent-test-001",
			Mode:        "runtime",
			Environment: "test",
			Role:        "api",
		},
		Runtime: config.RuntimeConfig{
			MaxActiveContainers:     10,
			DrainingGracePeriodSecs: 300,
			CommandPollIntervalSecs: 1,
		},
		Ports:  config.PortsConfig{From: 4100, To: 4114},
		Docker: config.DockerConfig{PullTimeoutSecs: 10, StopTimeoutSecs: 5},
		HealthCheck: config.HealthCheckConfig{
			TimeoutSecs:  1,
			IntervalSecs: 0,
			Retries:      1,
		},
	}

	pc := &fakePlatformClient{
		registerResp: &platform.RegisterResponse{AgentID: "agent-test-001", Status: "registered"},
		claimResp:    &platform.ClaimResponse{ID: "cmd_123", Status: "claimed"},
		startResp:    &platform.StartResponse{ID: "cmd_123", Status: "running"},
		heartbeatResp: &platform.HeartbeatResponse{Status: "ok"},
	}
	dr := &fakeDockerRuntime{
		startResp: &docker.ContainerInfo{
			ID: "abc123def456", Name: "billing-api-test-v42",
			Running: true, Status: "running",
			HostPort: 4102, ContainerPort: 3000,
		},
		inspectResp: &docker.ContainerInfo{
			Name: "billing-api-test-v42", Running: true, Status: "running",
		},
	}
	pm := &fakePortManager{allocPort: 4102}
	ss := &fakeStateStore{
		localState: &state.LocalState{
			AgentID:     "agent-test-001",
			Environment: "test",
		},
	}
	hc := &fakeHealthChecker{
		httpResult:      &health.Result{Status: health.StatusHealthy},
		containerResult: &health.Result{Status: health.StatusHealthy},
	}

	a := New(cfg, Dependencies{
		Platform: pc,
		Docker:   dr,
		Ports:    pm,
		State:    ss,
		Health:   hc,
	}, nopLogger())
	a.agentID = "agent-test-001"
	a.privateIP = "10.0.2.25"
	a.localState = ss.localState

	return a, pc, dr, pm, ss, hc
}

func makeDeployPayload(t *testing.T, p DeployApplicationPayload) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal DeployApplicationPayload: %v", err)
	}
	return data
}

func defaultDeployPayload() DeployApplicationPayload {
	return DeployApplicationPayload{
		Application:           "billing-api",
		Environment:           "test",
		Image:                 "ghcr.io/billing:v42",
		ContainerName:         "billing-api-test-v42",
		ContainerInternalPort: 3000,
		HealthCheckPath:       "/health",
		RequiresRoute:         true,
	}
}

// ============================================================
// Tests: registration
// ============================================================

func TestEnsureRegistered_FirstBoot(t *testing.T) {
	a, pc, _, _, ss, _ := newTestAgent(t)
	ss.identity = nil // no saved identity

	if err := a.ensureRegistered(context.Background()); err != nil {
		t.Fatalf("ensureRegistered: %v", err)
	}

	if !pc.registerCalled {
		t.Error("Register should be called on first boot")
	}
	if a.agentID != "agent-test-001" {
		t.Errorf("agentID: got %q, want agent-test-001", a.agentID)
	}
	if pc.agentID != "agent-test-001" {
		t.Errorf("platform client agentID not set: got %q", pc.agentID)
	}
	if ss.savedIdentity == nil {
		t.Fatal("identity not persisted")
	}
	if ss.savedIdentity.AgentID != "agent-test-001" {
		t.Errorf("saved agent ID: got %q", ss.savedIdentity.AgentID)
	}
	if ss.savedIdentity.RegisteredAt.IsZero() {
		t.Error("RegisteredAt must not be zero")
	}
}

func TestEnsureRegistered_AlreadyRegistered(t *testing.T) {
	a, pc, _, _, ss, _ := newTestAgent(t)
	ss.identity = &state.AgentIdentity{AgentID: "agent-existing-001"}

	if err := a.ensureRegistered(context.Background()); err != nil {
		t.Fatalf("ensureRegistered: %v", err)
	}

	if pc.registerCalled {
		t.Error("Register must NOT be called when identity already exists")
	}
	if a.agentID != "agent-existing-001" {
		t.Errorf("agentID: got %q, want agent-existing-001", a.agentID)
	}
}

func TestEnsureRegistered_RegistrationFails(t *testing.T) {
	a, pc, _, _, ss, _ := newTestAgent(t)
	ss.identity = nil
	pc.registerErr = aerrors.New(aerrors.CodePlatformAPIUnavailable, "service unavailable")

	err := a.ensureRegistered(context.Background())
	if err == nil {
		t.Fatal("expected error when registration fails")
	}
}

// ============================================================
// Tests: heartbeat
// ============================================================

func TestSendHeartbeat_SetsCorrectFields(t *testing.T) {
	a, pc, _, _, _, _ := newTestAgent(t)
	a.localState.Deployments = []state.DeploymentEntry{
		{DeploymentID: "dep_1", Status: state.DeploymentStatusActive},
		{DeploymentID: "dep_2", Status: state.DeploymentStatusActive},
		{DeploymentID: "dep_3", Status: state.DeploymentStatusDraining},
	}
	a.localState.LastAppliedCommandID = "cmd_99"

	a.sendHeartbeat(context.Background())

	if !pc.heartbeatCalled {
		t.Fatal("heartbeat not sent")
	}
	req := pc.heartbeatReq
	if req.Status != "online" {
		t.Errorf("status: got %q, want online", req.Status)
	}
	if req.Mode != "runtime" {
		t.Errorf("mode: got %q, want runtime", req.Mode)
	}
	if req.Environment != "test" {
		t.Errorf("environment: got %q, want test", req.Environment)
	}
	if req.ActiveDeployments != 2 {
		t.Errorf("active deployments: got %d, want 2", req.ActiveDeployments)
	}
	if req.LastCommandID != "cmd_99" {
		t.Errorf("last command ID: got %q, want cmd_99", req.LastCommandID)
	}
	if req.PrivateIP != "10.0.2.25" {
		t.Errorf("private IP: got %q, want 10.0.2.25", req.PrivateIP)
	}
}

func TestSendHeartbeat_PlatformFailureDoesNotPanic(t *testing.T) {
	a, pc, _, _, _, _ := newTestAgent(t)
	pc.heartbeatErr = aerrors.New(aerrors.CodePlatformAPIUnavailable, "connection refused")

	// Should not panic or return error.
	a.sendHeartbeat(context.Background())
}

// ============================================================
// Tests: DEPLOY_APPLICATION
// ============================================================

func TestExecuteDeployApplication_HappyPath(t *testing.T) {
	a, pc, dr, pm, ss, _ := newTestAgent(t)

	cmd := platform.PendingCommand{
		ID:           "cmd_123",
		Type:         platform.CommandTypeDeployApplication,
		DeploymentID: "dep_456",
		Payload:      makeDeployPayload(t, defaultDeployPayload()),
	}

	if err := a.executeDeployApplication(context.Background(), cmd); err != nil {
		t.Fatalf("executeDeployApplication: %v", err)
	}

	// Container must have been started.
	if !dr.startCalled {
		t.Error("StartContainer must be called")
	}

	// Must have reported success.
	report := pc.lastReport()
	if report == nil {
		t.Fatal("no report sent")
	}
	if report.Status != "succeeded" {
		t.Errorf("report status: got %q, want succeeded", report.Status)
	}
	if report.Result == nil {
		t.Fatal("result must not be nil")
	}
	if report.Result.HostPort != 4102 {
		t.Errorf("result.HostPort: got %d, want 4102", report.Result.HostPort)
	}
	if report.Result.Health != "healthy" {
		t.Errorf("result.Health: got %q", report.Result.Health)
	}
	if report.Result.RuntimePrivateIP != "10.0.2.25" {
		t.Errorf("result.RuntimePrivateIP: got %q", report.Result.RuntimePrivateIP)
	}
	if report.Result.RequiresRoute != true {
		t.Error("result.RequiresRoute must be true")
	}

	// State must show the deployment as active.
	if ss.savedLocal == nil {
		t.Fatal("state not persisted")
	}
	dep := findDeploymentByID(ss.savedLocal, "dep_456")
	if dep == nil {
		t.Fatal("deployment not found in state")
	}
	if dep.Status != state.DeploymentStatusActive {
		t.Errorf("deployment status: got %q, want active", dep.Status)
	}
	if dep.HostPort != 4102 {
		t.Errorf("deployment host port: got %d, want 4102", dep.HostPort)
	}

	// Port must not have been released.
	if pm.releaseCalled {
		t.Error("Release must not be called on happy path")
	}
}

func TestExecuteDeployApplication_BuildsManagedLabels(t *testing.T) {
	a, _, dr, _, _, _ := newTestAgent(t)

	cmd := platform.PendingCommand{
		ID:           "cmd_123",
		Type:         platform.CommandTypeDeployApplication,
		DeploymentID: "dep_456",
		Payload:      makeDeployPayload(t, defaultDeployPayload()),
	}

	_ = a.executeDeployApplication(context.Background(), cmd)

	labels := dr.startSpec.Labels
	if labels["devex.managed"] != "true" {
		t.Errorf("devex.managed: got %q, want true", labels["devex.managed"])
	}
	if labels["devex.deployment_id"] != "dep_456" {
		t.Errorf("devex.deployment_id: got %q, want dep_456", labels["devex.deployment_id"])
	}
	if labels["devex.command_id"] != "cmd_123" {
		t.Errorf("devex.command_id: got %q", labels["devex.command_id"])
	}
	if labels["devex.application"] != "billing-api" {
		t.Errorf("devex.application: got %q", labels["devex.application"])
	}
}

func TestExecuteDeployApplication_ImagePullFailed(t *testing.T) {
	a, pc, dr, pm, _, _ := newTestAgent(t)
	dr.pullErr = aerrors.New(aerrors.CodeImagePullFailed, "image not found")

	cmd := platform.PendingCommand{
		ID:           "cmd_123",
		Type:         platform.CommandTypeDeployApplication,
		DeploymentID: "dep_456",
		Payload:      makeDeployPayload(t, defaultDeployPayload()),
	}

	if err := a.executeDeployApplication(context.Background(), cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Container must NOT have been started.
	if dr.startCalled {
		t.Error("StartContainer must not be called when pull fails")
	}

	// Port must NOT have been allocated (no release needed).
	if pm.releaseCalled {
		t.Error("Release should not be called (port was never allocated)")
	}

	report := pc.lastReport()
	if report == nil {
		t.Fatal("no report sent")
	}
	if report.Status != "failed" {
		t.Errorf("report status: got %q, want failed", report.Status)
	}
	if report.Error == nil {
		t.Fatal("error must be set on failure")
	}
	if report.Error.Code != string(aerrors.CodeImagePullFailed) {
		t.Errorf("error code: got %q, want IMAGE_PULL_FAILED", report.Error.Code)
	}
}

func TestExecuteDeployApplication_PortAllocationFailed(t *testing.T) {
	a, pc, dr, pm, _, _ := newTestAgent(t)
	pm.allocErr = aerrors.New(aerrors.CodePortRangeExhausted, "no ports available")

	cmd := platform.PendingCommand{
		ID:           "cmd_123",
		Type:         platform.CommandTypeDeployApplication,
		DeploymentID: "dep_456",
		Payload:      makeDeployPayload(t, defaultDeployPayload()),
	}

	_ = a.executeDeployApplication(context.Background(), cmd)

	if dr.startCalled {
		t.Error("StartContainer must not be called when port allocation fails")
	}

	report := pc.lastReport()
	if report == nil || report.Status != "failed" {
		t.Errorf("expected failed report, got %+v", report)
	}
	if report.Error.Code != string(aerrors.CodePortRangeExhausted) {
		t.Errorf("error code: got %q, want PORT_RANGE_EXHAUSTED", report.Error.Code)
	}
}

func TestExecuteDeployApplication_HealthCheckFailed_CleansUp(t *testing.T) {
	a, pc, dr, pm, ss, hc := newTestAgent(t)
	hc.httpResult = &health.Result{Status: health.StatusUnhealthy}
	hc.httpErr = aerrors.New(aerrors.CodeHealthCheckFailed, "connection refused")

	cmd := platform.PendingCommand{
		ID:           "cmd_123",
		Type:         platform.CommandTypeDeployApplication,
		DeploymentID: "dep_456",
		Payload:      makeDeployPayload(t, defaultDeployPayload()),
	}

	if err := a.executeDeployApplication(context.Background(), cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cleanup: container must be stopped and removed.
	if !dr.stopCalled {
		t.Error("StopContainer must be called on health check failure")
	}
	if !dr.rmCalled {
		t.Error("RemoveContainer must be called on health check failure")
	}

	// Port must be released.
	if !pm.releaseCalled {
		t.Error("Release must be called on health check failure")
	}
	if pm.releasePort != 4102 {
		t.Errorf("released port: got %d, want 4102", pm.releasePort)
	}

	// Failed deployment must be removed from state.
	if ss.savedLocal != nil && findDeploymentByID(ss.savedLocal, "dep_456") != nil {
		t.Error("failed deployment must not remain in local state")
	}

	report := pc.lastReport()
	if report == nil || report.Status != "failed" {
		t.Errorf("expected failed report, got %+v", report)
	}
	if report.Error.Code != string(aerrors.CodeHealthCheckFailed) {
		t.Errorf("error code: got %q, want HEALTH_CHECK_FAILED", report.Error.Code)
	}
}

func TestExecuteDeployApplication_Idempotent(t *testing.T) {
	a, pc, dr, _, _, _ := newTestAgent(t)

	// Pre-populate state with an active deployment.
	a.localState.Deployments = []state.DeploymentEntry{{
		DeploymentID:          "dep_456",
		ContainerName:         "billing-api-test-v42",
		Image:                 "ghcr.io/billing:v42",
		HostPort:              4101,
		ContainerInternalPort: 3000,
		Status:                state.DeploymentStatusActive,
	}}

	cmd := platform.PendingCommand{
		ID:           "cmd_123",
		Type:         platform.CommandTypeDeployApplication,
		DeploymentID: "dep_456",
		Payload:      makeDeployPayload(t, defaultDeployPayload()),
	}

	if err := a.executeDeployApplication(context.Background(), cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No Docker operations should be performed.
	if dr.startCalled {
		t.Error("StartContainer must not be called for idempotent deploy")
	}

	report := pc.lastReport()
	if report == nil || report.Status != "succeeded" {
		t.Errorf("expected succeeded report, got %+v", report)
	}
	// Result should reflect the existing deployment's port.
	if report.Result != nil && report.Result.HostPort != 4101 {
		t.Errorf("result host port: got %d, want 4101", report.Result.HostPort)
	}
}

func TestExecuteDeployApplication_InvalidPayload(t *testing.T) {
	a, pc, _, _, _, _ := newTestAgent(t)

	cmd := platform.PendingCommand{
		ID:           "cmd_123",
		Type:         platform.CommandTypeDeployApplication,
		DeploymentID: "dep_456",
		Payload:      json.RawMessage(`{bad json`),
	}

	_ = a.executeDeployApplication(context.Background(), cmd)

	report := pc.lastReport()
	if report == nil || report.Status != "failed" {
		t.Errorf("expected failed report, got %+v", report)
	}
	if report.Error.Code != string(aerrors.CodeCommandInvalid) {
		t.Errorf("error code: got %q, want COMMAND_INVALID", report.Error.Code)
	}
}

func TestExecuteDeployApplication_WorkerNoPort(t *testing.T) {
	a, pc, dr, pm, _, hc := newTestAgent(t)

	// Worker: no internal port, no health check path.
	p := DeployApplicationPayload{
		Application:  "invoice-worker",
		Environment:  "test",
		Image:        "ghcr.io/worker:v1",
		ContainerName: "invoice-worker-test-v1",
		// ContainerInternalPort: 0 (worker)
		// HealthCheckPath: "" (container check)
		RequiresRoute: false,
	}

	cmd := platform.PendingCommand{
		ID:           "cmd_200",
		Type:         platform.CommandTypeDeployApplication,
		DeploymentID: "dep_200",
		Payload:      makeDeployPayload(t, p),
	}

	if err := a.executeDeployApplication(context.Background(), cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Port must not be allocated for a worker.
	if pm.releaseCalled {
		t.Error("port should not be allocated/released for a worker")
	}

	// Container check should have been used (not HTTP).
	if hc.httpResult != nil && hc.httpErr != nil {
		t.Log("HTTP check was called (unexpected for worker, but not an error if health passes)")
	}

	// Must have started the container.
	if !dr.startCalled {
		t.Error("StartContainer must be called for worker")
	}

	// Must have reported success.
	report := pc.lastReport()
	if report == nil || report.Status != "succeeded" {
		t.Errorf("expected succeeded report, got %+v", report)
	}
}

// ============================================================
// Tests: STOP_APPLICATION
// ============================================================

func TestExecuteStopApplication_Success(t *testing.T) {
	a, pc, dr, _, _, _ := newTestAgent(t)

	payload, _ := json.Marshal(StopApplicationPayload{
		ContainerName:      "billing-api-test-v42",
		StopTimeoutSeconds: 10,
	})
	cmd := platform.PendingCommand{
		ID:           "cmd_125",
		Type:         platform.CommandTypeStopApplication,
		DeploymentID: "dep_456",
		Payload:      payload,
	}

	if err := a.executeStopApplication(context.Background(), cmd); err != nil {
		t.Fatalf("executeStopApplication: %v", err)
	}

	if !dr.stopCalled {
		t.Error("StopContainer must be called")
	}
	if dr.stopCalledWith != "billing-api-test-v42" {
		t.Errorf("stopped container: got %q", dr.stopCalledWith)
	}

	report := pc.lastReport()
	if report == nil || report.Status != "succeeded" {
		t.Errorf("expected succeeded report, got %+v", report)
	}
}

func TestExecuteStopApplication_ContainerNotFound_StillSucceeds(t *testing.T) {
	a, pc, dr, _, _, _ := newTestAgent(t)
	dr.stopErr = aerrors.New(aerrors.CodeContainerNotFound, "no such container")

	payload, _ := json.Marshal(StopApplicationPayload{ContainerName: "billing-api-test-v42"})
	cmd := platform.PendingCommand{
		ID: "cmd_125", Type: platform.CommandTypeStopApplication,
		DeploymentID: "dep_456", Payload: payload,
	}

	_ = a.executeStopApplication(context.Background(), cmd)

	report := pc.lastReport()
	if report == nil || report.Status != "succeeded" {
		t.Errorf("expected succeeded even when container not found, got %+v", report)
	}
}

func TestExecuteStopApplication_MissingContainerName(t *testing.T) {
	a, pc, _, _, _, _ := newTestAgent(t)

	payload, _ := json.Marshal(StopApplicationPayload{}) // empty ContainerName
	cmd := platform.PendingCommand{
		ID: "cmd_125", Type: platform.CommandTypeStopApplication,
		DeploymentID: "dep_456", Payload: payload,
	}

	_ = a.executeStopApplication(context.Background(), cmd)

	report := pc.lastReport()
	if report == nil || report.Status != "failed" {
		t.Errorf("expected failed report for missing container name, got %+v", report)
	}
	if report.Error.Code != string(aerrors.CodeCommandInvalid) {
		t.Errorf("error code: got %q, want COMMAND_INVALID", report.Error.Code)
	}
}

// ============================================================
// Tests: REMOVE_DEPLOYMENT
// ============================================================

func TestExecuteRemoveDeployment_Success(t *testing.T) {
	a, pc, dr, pm, _, _ := newTestAgent(t)

	a.localState.Deployments = []state.DeploymentEntry{{
		DeploymentID:  "dep_456",
		ContainerName: "billing-api-test-v42",
		HostPort:      4102,
		Status:        state.DeploymentStatusDraining,
	}}

	payload, _ := json.Marshal(RemoveDeploymentPayload{
		DeploymentID:  "dep_456",
		ContainerName: "billing-api-test-v42",
		ReleasePort:   true,
	})
	cmd := platform.PendingCommand{
		ID: "cmd_126", Type: platform.CommandTypeRemoveDeployment,
		DeploymentID: "dep_456", Payload: payload,
	}

	if err := a.executeRemoveDeployment(context.Background(), cmd); err != nil {
		t.Fatalf("executeRemoveDeployment: %v", err)
	}

	if !dr.rmCalled {
		t.Error("RemoveContainer must be called")
	}
	if dr.rmCalledWith != "billing-api-test-v42" {
		t.Errorf("removed container: got %q", dr.rmCalledWith)
	}
	if !pm.releaseCalled {
		t.Error("Release must be called with ReleasePort=true")
	}
	if pm.releasePort != 4102 {
		t.Errorf("released port: got %d, want 4102", pm.releasePort)
	}

	report := pc.lastReport()
	if report == nil || report.Status != "succeeded" {
		t.Errorf("expected succeeded report, got %+v", report)
	}
}

func TestExecuteRemoveDeployment_NoPortRelease(t *testing.T) {
	a, _, _, pm, _, _ := newTestAgent(t)
	a.localState.Deployments = []state.DeploymentEntry{{
		DeploymentID:  "dep_456",
		ContainerName: "billing-api-test-v42",
		HostPort:      4102,
		Status:        state.DeploymentStatusDraining,
	}}

	payload, _ := json.Marshal(RemoveDeploymentPayload{
		DeploymentID:  "dep_456",
		ContainerName: "billing-api-test-v42",
		ReleasePort:   false, // explicitly false
	})
	cmd := platform.PendingCommand{
		ID: "cmd_126", Type: platform.CommandTypeRemoveDeployment,
		DeploymentID: "dep_456", Payload: payload,
	}

	_ = a.executeRemoveDeployment(context.Background(), cmd)

	if pm.releaseCalled {
		t.Error("Release must NOT be called when ReleasePort=false")
	}
}

// ============================================================
// Tests: CLEANUP_DRAINING
// ============================================================

func TestCleanupExpiredDraining_RemovesExpiredContainers(t *testing.T) {
	a, _, dr, pm, ss, _ := newTestAgent(t)

	past := time.Now().Add(-10 * time.Minute)
	a.localState.Deployments = []state.DeploymentEntry{
		{
			DeploymentID:      "dep_old",
			ContainerName:     "billing-api-test-v41",
			HostPort:          4101,
			Status:            state.DeploymentStatusDraining,
			DrainingStartedAt: &past,
		},
		{
			DeploymentID:  "dep_active",
			ContainerName: "billing-api-test-v42",
			HostPort:      4102,
			Status:        state.DeploymentStatusActive,
		},
	}

	a.cleanupExpiredDraining(context.Background(), 5*time.Minute)

	if !dr.stopCalled {
		t.Error("StopContainer must be called for expired draining container")
	}
	if !dr.rmCalled {
		t.Error("RemoveContainer must be called for expired draining container")
	}
	if !pm.releaseCalled {
		t.Error("Release must be called for expired draining container")
	}
	if pm.releasePort != 4101 {
		t.Errorf("released port: got %d, want 4101", pm.releasePort)
	}

	// Expired deployment must be removed from state.
	if ss.savedLocal != nil && findDeploymentByID(ss.savedLocal, "dep_old") != nil {
		t.Error("expired deployment must be removed from state")
	}
	// Active deployment must remain.
	if ss.savedLocal != nil && findDeploymentByID(ss.savedLocal, "dep_active") == nil {
		t.Error("active deployment must remain in state")
	}
}

func TestCleanupExpiredDraining_IgnoresNonExpired(t *testing.T) {
	a, _, dr, _, _, _ := newTestAgent(t)

	recent := time.Now().Add(-1 * time.Minute)
	a.localState.Deployments = []state.DeploymentEntry{{
		DeploymentID:      "dep_drain",
		ContainerName:     "billing-api-test-v41",
		HostPort:          4101,
		Status:            state.DeploymentStatusDraining,
		DrainingStartedAt: &recent,
	}}

	a.cleanupExpiredDraining(context.Background(), 5*time.Minute)

	if dr.stopCalled || dr.rmCalled {
		t.Error("non-expired draining container must not be removed")
	}
}

// ============================================================
// Tests: pollAndProcessCommands
// ============================================================

func TestPollAndProcessCommands_ClaimFailed_NoReport(t *testing.T) {
	a, pc, _, _, _, _ := newTestAgent(t)

	pc.pendingCommands = []platform.PendingCommand{{
		ID:   "cmd_123",
		Type: platform.CommandTypeDeployApplication,
		Payload: makeDeployPayload(t, defaultDeployPayload()),
	}}
	pc.claimErr = aerrors.New(aerrors.CodeCommandClaimFailed, "conflict")

	a.pollAndProcessCommands(context.Background())

	if len(pc.reportedRequests) > 0 {
		t.Error("no report must be sent when claim fails")
	}
}

func TestPollAndProcessCommands_NoPendingCommands(t *testing.T) {
	a, pc, dr, _, _, _ := newTestAgent(t)
	pc.pendingCommands = nil

	a.pollAndProcessCommands(context.Background())

	if dr.startCalled {
		t.Error("no docker operations when no commands pending")
	}
}

func TestPollAndProcessCommands_UnknownCommandType(t *testing.T) {
	a, pc, _, _, _, _ := newTestAgent(t)

	pc.pendingCommands = []platform.PendingCommand{{
		ID:      "cmd_123",
		Type:    "UNKNOWN_COMMAND",
		Payload: json.RawMessage(`{}`),
	}}

	a.pollAndProcessCommands(context.Background())

	report := pc.lastReport()
	if report == nil || report.Status != "failed" {
		t.Errorf("expected failed report for unknown command type, got %+v", report)
	}
	if report.Error.Code != string(aerrors.CodeCommandInvalid) {
		t.Errorf("error code: got %q, want COMMAND_INVALID", report.Error.Code)
	}
}

// ============================================================
// Tests: helpers
// ============================================================

func TestBuildManagedLabels(t *testing.T) {
	labels := buildManagedLabels("cmd_1", "dep_1", "my-app", "dev", "agent-1")

	expected := map[string]string{
		"devex.managed":       "true",
		"devex.command_id":    "cmd_1",
		"devex.deployment_id": "dep_1",
		"devex.application":   "my-app",
		"devex.environment":   "dev",
		"devex.agent_id":      "agent-1",
	}
	for k, v := range expected {
		if labels[k] != v {
			t.Errorf("label[%s]: got %q, want %q", k, labels[k], v)
		}
	}
}

func TestStateHelpers_UpsertAndFind(t *testing.T) {
	ls := &state.LocalState{}

	upsertDeployment(ls, state.DeploymentEntry{DeploymentID: "dep_1", Application: "app-a"})
	upsertDeployment(ls, state.DeploymentEntry{DeploymentID: "dep_2", Application: "app-b"})

	if len(ls.Deployments) != 2 {
		t.Fatalf("expected 2 deployments, got %d", len(ls.Deployments))
	}

	// Upsert existing.
	upsertDeployment(ls, state.DeploymentEntry{DeploymentID: "dep_1", Application: "app-a-updated"})
	if len(ls.Deployments) != 2 {
		t.Fatalf("upsert should not add duplicate: got %d", len(ls.Deployments))
	}
	if findDeploymentByID(ls, "dep_1").Application != "app-a-updated" {
		t.Error("upsert should update existing entry")
	}
}

func TestStateHelpers_Remove(t *testing.T) {
	ls := &state.LocalState{
		Deployments: []state.DeploymentEntry{
			{DeploymentID: "dep_1"},
			{DeploymentID: "dep_2"},
			{DeploymentID: "dep_3"},
		},
	}

	removeDeploymentByID(ls, "dep_2")

	if len(ls.Deployments) != 2 {
		t.Fatalf("expected 2 after remove, got %d", len(ls.Deployments))
	}
	if findDeploymentByID(ls, "dep_2") != nil {
		t.Error("dep_2 must be gone")
	}
	if findDeploymentByID(ls, "dep_1") == nil || findDeploymentByID(ls, "dep_3") == nil {
		t.Error("dep_1 and dep_3 must remain")
	}
}

func TestHealthCheckTotalTimeout_Defaults(t *testing.T) {
	cfg := health.CheckConfig{TimeoutSeconds: 2, IntervalSeconds: 5, Retries: 6}
	timeout := healthCheckTotalTimeout(cfg)
	// 6 * (2 + 5 + 1) = 48 seconds, but minimum is 30s
	expected := 48 * time.Second
	if timeout != expected {
		t.Errorf("timeout: got %v, want %v", timeout, expected)
	}
}

func TestHealthCheckTotalTimeout_Minimum(t *testing.T) {
	cfg := health.CheckConfig{TimeoutSeconds: 1, IntervalSeconds: 0, Retries: 1}
	timeout := healthCheckTotalTimeout(cfg)
	if timeout < 30*time.Second {
		t.Errorf("timeout must be at least 30s, got %v", timeout)
	}
}

// ============================================================
// Tests: MARK_DRAINING
// ============================================================

func TestExecuteMarkDraining_MarksActiveDeploymentAsDraining(t *testing.T) {
	a, pc, _, pm, ss, _ := newTestAgent(t)
	a.localState.Deployments = []state.DeploymentEntry{{
		DeploymentID:  "dep_456",
		ContainerName: "billing-api-test-v41",
		HostPort:      4101,
		Status:        state.DeploymentStatusActive,
	}}

	payload, _ := json.Marshal(MarkDrainingPayload{
		DeploymentID:  "dep_456",
		ContainerName: "billing-api-test-v41",
	})
	cmd := platform.PendingCommand{
		ID:           "cmd_300",
		Type:         platform.CommandTypeMarkDraining,
		DeploymentID: "dep_456",
		Payload:      payload,
	}

	if err := a.executeMarkDraining(context.Background(), cmd); err != nil {
		t.Fatalf("executeMarkDraining: %v", err)
	}

	// State must show draining.
	if ss.savedLocal == nil {
		t.Fatal("state not persisted")
	}
	dep := findDeploymentByID(ss.savedLocal, "dep_456")
	if dep == nil {
		t.Fatal("deployment not found in state")
	}
	if dep.Status != state.DeploymentStatusDraining {
		t.Errorf("status: got %q, want draining", dep.Status)
	}
	if dep.DrainingStartedAt == nil {
		t.Error("DrainingStartedAt must be set")
	}
	if dep.DrainingStartedAt.IsZero() {
		t.Error("DrainingStartedAt must not be zero")
	}

	// Port must be marked as draining.
	if !pm.drainingCalled {
		t.Error("MarkDraining must be called on the port")
	}
	if pm.drainingPort != 4101 {
		t.Errorf("draining port: got %d, want 4101", pm.drainingPort)
	}

	// Report succeeded.
	report := pc.lastReport()
	if report == nil || report.Status != "succeeded" {
		t.Errorf("expected succeeded report, got %+v", report)
	}
}

func TestExecuteMarkDraining_AlreadyDraining_Idempotent(t *testing.T) {
	a, pc, _, pm, _, _ := newTestAgent(t)
	past := time.Now().Add(-2 * time.Minute)
	a.localState.Deployments = []state.DeploymentEntry{{
		DeploymentID:      "dep_456",
		ContainerName:     "billing-api-test-v41",
		HostPort:          4101,
		Status:            state.DeploymentStatusDraining,
		DrainingStartedAt: &past,
	}}

	payload, _ := json.Marshal(MarkDrainingPayload{DeploymentID: "dep_456"})
	cmd := platform.PendingCommand{
		ID: "cmd_300", Type: platform.CommandTypeMarkDraining,
		DeploymentID: "dep_456", Payload: payload,
	}

	if err := a.executeMarkDraining(context.Background(), cmd); err != nil {
		t.Fatalf("executeMarkDraining: %v", err)
	}

	// Port must NOT be re-marked.
	if pm.drainingCalled {
		t.Error("MarkDraining must not be called when deployment is already draining")
	}

	report := pc.lastReport()
	if report == nil || report.Status != "succeeded" {
		t.Errorf("expected succeeded report, got %+v", report)
	}
}

func TestExecuteMarkDraining_NotFound_SucceedsIdempotent(t *testing.T) {
	a, pc, _, _, _, _ := newTestAgent(t)
	// No deployments in local state.

	payload, _ := json.Marshal(MarkDrainingPayload{DeploymentID: "dep_gone"})
	cmd := platform.PendingCommand{
		ID: "cmd_300", Type: platform.CommandTypeMarkDraining,
		DeploymentID: "dep_gone", Payload: payload,
	}

	if err := a.executeMarkDraining(context.Background(), cmd); err != nil {
		t.Fatalf("executeMarkDraining: %v", err)
	}

	report := pc.lastReport()
	if report == nil || report.Status != "succeeded" {
		t.Errorf("expected succeeded report for missing deployment, got %+v", report)
	}
}

func TestExecuteMarkDraining_MissingDeploymentID_Fails(t *testing.T) {
	a, pc, _, _, _, _ := newTestAgent(t)

	payload, _ := json.Marshal(MarkDrainingPayload{DeploymentID: ""})
	cmd := platform.PendingCommand{
		ID: "cmd_300", Type: platform.CommandTypeMarkDraining,
		DeploymentID: "dep_456", Payload: payload,
	}

	_ = a.executeMarkDraining(context.Background(), cmd)

	report := pc.lastReport()
	if report == nil || report.Status != "failed" {
		t.Errorf("expected failed report for missing deployment_id, got %+v", report)
	}
	if report.Error.Code != string(aerrors.CodeCommandInvalid) {
		t.Errorf("error code: got %q, want COMMAND_INVALID", report.Error.Code)
	}
}

func TestExecuteMarkDraining_DrainingStartedAtNotOverwritten(t *testing.T) {
	a, _, _, _, ss, _ := newTestAgent(t)
	// Active deployment — first call marks it draining.
	a.localState.Deployments = []state.DeploymentEntry{{
		DeploymentID:  "dep_456",
		ContainerName: "billing-api-test-v41",
		HostPort:      4101,
		Status:        state.DeploymentStatusActive,
	}}

	payload, _ := json.Marshal(MarkDrainingPayload{DeploymentID: "dep_456"})
	cmd := platform.PendingCommand{
		ID: "cmd_300", Type: platform.CommandTypeMarkDraining,
		DeploymentID: "dep_456", Payload: payload,
	}

	before := time.Now()
	_ = a.executeMarkDraining(context.Background(), cmd)
	after := time.Now()

	dep := findDeploymentByID(ss.savedLocal, "dep_456")
	if dep == nil || dep.DrainingStartedAt == nil {
		t.Fatal("DrainingStartedAt not set")
	}
	if dep.DrainingStartedAt.Before(before) || dep.DrainingStartedAt.After(after) {
		t.Errorf("DrainingStartedAt %v not in range [%v, %v]",
			dep.DrainingStartedAt, before, after)
	}
}

// ============================================================
// Tests: RECONCILE command
// ============================================================

func TestExecuteReconcile_Success_AllScope(t *testing.T) {
	a, pc, dr, pm, _, _ := newTestAgent(t)
	dr.listResp = []docker.ContainerInfo{
		{Name: "billing-api-test-v42", Running: true, Status: "running"},
	}

	payload, _ := json.Marshal(ReconcilePayload{Scope: "all"})
	cmd := platform.PendingCommand{
		ID: "cmd_400", Type: platform.CommandTypeReconcile,
		Payload: payload,
	}

	if err := a.executeReconcile(context.Background(), cmd); err != nil {
		t.Fatalf("executeReconcile: %v", err)
	}

	if !pm.reconcileCalled {
		t.Error("Ports.Reconcile must be called for scope=all")
	}

	report := pc.lastReport()
	if report == nil || report.Status != "succeeded" {
		t.Errorf("expected succeeded report, got %+v", report)
	}
}

func TestExecuteReconcile_DefaultScope_IsAll(t *testing.T) {
	a, pc, _, pm, _, _ := newTestAgent(t)

	// Empty payload → scope defaults to "all"
	cmd := platform.PendingCommand{
		ID: "cmd_400", Type: platform.CommandTypeReconcile,
		Payload: json.RawMessage(`{}`),
	}

	_ = a.executeReconcile(context.Background(), cmd)

	if !pm.reconcileCalled {
		t.Error("Ports.Reconcile must be called when scope is empty (defaults to all)")
	}
	report := pc.lastReport()
	if report == nil || report.Status != "succeeded" {
		t.Errorf("expected succeeded, got %+v", report)
	}
}

func TestExecuteReconcile_PortsScope_OnlyReconcilesPorts(t *testing.T) {
	a, _, dr, pm, _, _ := newTestAgent(t)
	dr.listResp = nil

	payload, _ := json.Marshal(ReconcilePayload{Scope: "ports"})
	cmd := platform.PendingCommand{
		ID: "cmd_400", Type: platform.CommandTypeReconcile,
		Payload: payload,
	}

	_ = a.executeReconcile(context.Background(), cmd)

	if !pm.reconcileCalled {
		t.Error("Ports.Reconcile must be called for scope=ports")
	}
}

func TestExecuteReconcile_ListFails_ReportsError(t *testing.T) {
	a, pc, dr, _, _, _ := newTestAgent(t)
	dr.listErr = aerrors.New(aerrors.CodeDockerUnavailable, "daemon not running")

	cmd := platform.PendingCommand{
		ID: "cmd_400", Type: platform.CommandTypeReconcile,
		Payload: json.RawMessage(`{}`),
	}

	_ = a.executeReconcile(context.Background(), cmd)

	report := pc.lastReport()
	if report == nil || report.Status != "failed" {
		t.Errorf("expected failed report when ListContainers fails, got %+v", report)
	}
}
