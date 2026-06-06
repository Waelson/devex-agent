package agent

import (
	"context"
	"io"
	"log/slog"
	"testing"

	aerrors "devex-agent/internal/errors"
	"devex-agent/internal/config"
	"devex-agent/internal/health"
	"devex-agent/internal/platform"
	"devex-agent/internal/state"
)

// ============================================================
// Fake implementations
// ============================================================

// --- fakeGatewayPlatformClient ---

type fakeGatewayPlatformClient struct {
	agentID string

	registerCalled bool
	registerResp   *platform.RegisterResponse
	registerErr    error

	heartbeatCalled bool
	heartbeatReq    platform.HeartbeatRequest
	heartbeatErr    error

	desiredState    *platform.DesiredState
	fetchErr        error

	reportedState []platform.DesiredStateReportRequest
	reportErr     error
}

func (f *fakeGatewayPlatformClient) SetAgentID(id string) { f.agentID = id }

func (f *fakeGatewayPlatformClient) Register(_ context.Context, _ platform.RegisterRequest) (*platform.RegisterResponse, error) {
	f.registerCalled = true
	return f.registerResp, f.registerErr
}

func (f *fakeGatewayPlatformClient) SendHeartbeat(_ context.Context, req platform.HeartbeatRequest) (*platform.HeartbeatResponse, error) {
	f.heartbeatCalled = true
	f.heartbeatReq = req
	return &platform.HeartbeatResponse{Status: "ok"}, f.heartbeatErr
}

func (f *fakeGatewayPlatformClient) FetchDesiredState(_ context.Context) (*platform.DesiredState, error) {
	return f.desiredState, f.fetchErr
}

func (f *fakeGatewayPlatformClient) ReportDesiredState(_ context.Context, req platform.DesiredStateReportRequest) error {
	f.reportedState = append(f.reportedState, req)
	return f.reportErr
}

func (f *fakeGatewayPlatformClient) lastReport() *platform.DesiredStateReportRequest {
	if len(f.reportedState) == 0 {
		return nil
	}
	r := f.reportedState[len(f.reportedState)-1]
	return &r
}

// --- fakeCaddyAdminClient ---

type fakeCaddyAdminClient struct {
	pingErr  error
	loadErr  error
	loadCalled bool
	savedConfigs map[string][]byte
	configs      map[string][]byte
}

func newFakeCaddyClient() *fakeCaddyAdminClient {
	return &fakeCaddyAdminClient{
		savedConfigs: map[string][]byte{},
		configs:      map[string][]byte{},
	}
}

func (f *fakeCaddyAdminClient) Ping(_ context.Context) error { return f.pingErr }

func (f *fakeCaddyAdminClient) Load(_ context.Context, configJSON []byte) error {
	f.loadCalled = true
	if f.loadErr != nil {
		return f.loadErr
	}
	f.savedConfigs["active"] = configJSON
	return nil
}

func (f *fakeCaddyAdminClient) SaveConfig(path string, data []byte) error {
	f.configs[path] = data
	return nil
}

func (f *fakeCaddyAdminClient) LoadConfig(path string) ([]byte, error) {
	data, ok := f.configs[path]
	if !ok {
		return nil, nil
	}
	return data, nil
}

// --- fakeCaddyConfigGenerator ---

type fakeCaddyConfigGenerator struct {
	generateErr  error
	emergencyErr error
}

func (f *fakeCaddyConfigGenerator) GenerateJSON(_ []platform.DesiredRoute) ([]byte, error) {
	if f.generateErr != nil {
		return nil, f.generateErr
	}
	return []byte(`{"admin":{"listen":"0.0.0.0:2019"},"apps":{}}`), nil
}

func (f *fakeCaddyConfigGenerator) EmergencyConfigJSON() ([]byte, error) {
	if f.emergencyErr != nil {
		return nil, f.emergencyErr
	}
	return []byte(`{"admin":{},"emergency":true}`), nil
}

// --- fakeGatewayHealthChecker ---

type fakeGatewayHealthChecker struct {
	result *health.Result
	err    error
}

func (f *fakeGatewayHealthChecker) CheckHTTP(_ context.Context, _ health.HTTPCheckTarget) (*health.Result, error) {
	return f.result, f.err
}

// --- fakeGatewayStateStore ---

type fakeGatewayStateStore struct {
	identity      *state.AgentIdentity
	savedIdentity *state.AgentIdentity
	saveIdErr     error
}

func (f *fakeGatewayStateStore) LoadAgentIdentity() (*state.AgentIdentity, error) {
	return f.identity, nil
}
func (f *fakeGatewayStateStore) SaveAgentIdentity(id *state.AgentIdentity) error {
	f.savedIdentity = id
	f.identity = id
	return f.saveIdErr
}
func (f *fakeGatewayStateStore) LoadLocalState() (*state.LocalState, error) {
	return &state.LocalState{}, nil
}
func (f *fakeGatewayStateStore) SaveLocalState(_ *state.LocalState) error { return nil }

// ============================================================
// Test helpers
// ============================================================

func newTestGatewayAgent(t *testing.T) (*GatewayAgent, *fakeGatewayPlatformClient, *fakeCaddyAdminClient, *fakeCaddyConfigGenerator, *fakeGatewayHealthChecker, *fakeGatewayStateStore) {
	t.Helper()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:          "agent-gw-001",
			Mode:        "gateway",
			Environment: "test",
			Role:        "gateway",
		},
		Caddy: config.CaddyConfig{
			AdminURL:                   "http://127.0.0.1:2019",
			CurrentConfigPath:          "/tmp/test-current-caddy.json",
			PreviousConfigPath:         "/tmp/test-previous-caddy.json",
			LastGoodConfigPath:         "/tmp/test-last-good-caddy.json",
			LoadTimeoutSecs:            5,
			RouteValidationTimeoutSecs: 2,
		},
		Reconcile: config.ReconcileConfig{IntervalSecs: 1},
	}

	pc := &fakeGatewayPlatformClient{
		registerResp: &platform.RegisterResponse{AgentID: "agent-gw-001", Status: "registered"},
		desiredState: &platform.DesiredState{
			Version:     1,
			Type:        "gateway_routes",
			Environment: "test",
			Routes: []platform.DesiredRoute{
				{
					ID:              "route_001",
					Host:            "billing-api.dev.example.com",
					Path:            "/",
					Upstream:        "10.0.2.25:4102",
					HealthCheckPath: "/health",
				},
			},
		},
	}
	cc := newFakeCaddyClient()
	gen := &fakeCaddyConfigGenerator{}
	hc := &fakeGatewayHealthChecker{
		result: &health.Result{Status: health.StatusHealthy},
	}
	ss := &fakeGatewayStateStore{}

	a := NewGateway(cfg, GatewayDependencies{
		Platform:  pc,
		Caddy:     cc,
		Generator: gen,
		State:     ss,
		Health:    hc,
	}, slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
	a.agentID = "agent-gw-001"
	a.privateIP = "10.0.1.10"

	return a, pc, cc, gen, hc, ss
}

// ============================================================
// Registration tests
// ============================================================

func TestGatewayAgent_EnsureRegistered_FirstBoot(t *testing.T) {
	a, pc, _, _, _, ss := newTestGatewayAgent(t)
	ss.identity = nil

	if err := a.ensureRegistered(context.Background()); err != nil {
		t.Fatalf("ensureRegistered: %v", err)
	}

	if !pc.registerCalled {
		t.Error("Register must be called on first boot")
	}
	if a.agentID != "agent-gw-001" {
		t.Errorf("agentID: got %q", a.agentID)
	}
	if pc.agentID != "agent-gw-001" {
		t.Errorf("platform agentID not set: got %q", pc.agentID)
	}
	if ss.savedIdentity == nil {
		t.Fatal("identity not persisted")
	}
	if ss.savedIdentity.AgentID != "agent-gw-001" {
		t.Errorf("saved agent ID: got %q", ss.savedIdentity.AgentID)
	}
	if ss.savedIdentity.RegisteredAt.IsZero() {
		t.Error("RegisteredAt must not be zero")
	}
}

func TestGatewayAgent_EnsureRegistered_AlreadyRegistered(t *testing.T) {
	a, pc, _, _, _, ss := newTestGatewayAgent(t)
	ss.identity = &state.AgentIdentity{AgentID: "agent-gw-existing"}

	if err := a.ensureRegistered(context.Background()); err != nil {
		t.Fatalf("ensureRegistered: %v", err)
	}

	if pc.registerCalled {
		t.Error("Register must NOT be called when identity already exists")
	}
	if a.agentID != "agent-gw-existing" {
		t.Errorf("agentID: got %q, want agent-gw-existing", a.agentID)
	}
}

func TestGatewayAgent_EnsureRegistered_RegistrationFails(t *testing.T) {
	a, pc, _, _, _, ss := newTestGatewayAgent(t)
	ss.identity = nil
	pc.registerErr = aerrors.New(aerrors.CodePlatformAPIUnavailable, "connection refused")

	err := a.ensureRegistered(context.Background())
	if err == nil {
		t.Fatal("expected error when registration fails")
	}
}

// ============================================================
// Heartbeat tests
// ============================================================

func TestGatewayAgent_SendHeartbeat_Fields(t *testing.T) {
	a, pc, _, _, _, _ := newTestGatewayAgent(t)
	a.lastAttemptedVersion = 42
	a.lastSuccessfulVersion = 41
	a.routesTotal = 3
	a.caddyStatus = "healthy"

	a.sendHeartbeat(context.Background())

	if !pc.heartbeatCalled {
		t.Fatal("heartbeat not sent")
	}
	req := pc.heartbeatReq
	if req.Mode != "gateway" {
		t.Errorf("mode: got %q, want gateway", req.Mode)
	}
	if req.Environment != "test" {
		t.Errorf("environment: got %q, want test", req.Environment)
	}
	if req.Status != "online" {
		t.Errorf("status: got %q, want online", req.Status)
	}
	if req.RoutesTotal != 3 {
		t.Errorf("routes_total: got %d, want 3", req.RoutesTotal)
	}
	if req.LastAppliedDesiredStateVersion != 42 {
		t.Errorf("last_applied_version: got %d, want 42", req.LastAppliedDesiredStateVersion)
	}
	if req.LastSuccessfulDesiredStateVersion != 41 {
		t.Errorf("last_successful_version: got %d, want 41", req.LastSuccessfulDesiredStateVersion)
	}
	if req.PrivateIP != "10.0.1.10" {
		t.Errorf("private_ip: got %q, want 10.0.1.10", req.PrivateIP)
	}
}

func TestGatewayAgent_SendHeartbeat_PlatformFailureDoesNotPanic(t *testing.T) {
	a, pc, _, _, _, _ := newTestGatewayAgent(t)
	pc.heartbeatErr = aerrors.New(aerrors.CodePlatformAPIUnavailable, "timeout")

	// Must not panic.
	a.sendHeartbeat(context.Background())
}

// ============================================================
// ReconcileOnce tests
// ============================================================

func TestGatewayAgent_ReconcileOnce_VersionUnchanged_Skips(t *testing.T) {
	a, pc, cc, _, _, _ := newTestGatewayAgent(t)
	a.lastSuccessfulVersion = 1 // same as desiredState.Version

	if err := a.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcileOnce: %v", err)
	}

	// No apply should have happened.
	if cc.loadCalled {
		t.Error("Caddy Load must not be called when version is unchanged")
	}
	if pc.lastReport() != nil {
		t.Error("no report must be sent when version is unchanged")
	}
}

func TestGatewayAgent_ReconcileOnce_AppliesNewVersion(t *testing.T) {
	a, pc, cc, _, _, _ := newTestGatewayAgent(t)
	a.lastSuccessfulVersion = 0 // older than version 1

	if err := a.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcileOnce: %v", err)
	}

	if !cc.loadCalled {
		t.Error("Caddy Load must be called for a new version")
	}

	report := pc.lastReport()
	if report == nil {
		t.Fatal("no report sent")
	}
	if report.Status != "applied" {
		t.Errorf("report status: got %q, want applied", report.Status)
	}
	if report.DesiredStateVersion != 1 {
		t.Errorf("report version: got %d, want 1", report.DesiredStateVersion)
	}
	if report.RoutesTotal != 1 {
		t.Errorf("routes_total: got %d, want 1", report.RoutesTotal)
	}
	if report.Type != "gateway_routes" {
		t.Errorf("type: got %q, want gateway_routes", report.Type)
	}

	// State must be updated.
	a.mu.Lock()
	lastSuccessful := a.lastSuccessfulVersion
	a.mu.Unlock()
	if lastSuccessful != 1 {
		t.Errorf("lastSuccessfulVersion: got %d, want 1", lastSuccessful)
	}
}

func TestGatewayAgent_ReconcileOnce_FetchFails_NonFatal(t *testing.T) {
	a, pc, cc, _, _, _ := newTestGatewayAgent(t)
	pc.fetchErr = aerrors.New(aerrors.CodeDesiredStateFetchFailed, "connection refused")

	err := a.reconcileOnce(context.Background())
	if err != nil {
		t.Errorf("reconcileOnce should return nil on fetch failure, got: %v", err)
	}
	if cc.loadCalled {
		t.Error("Caddy Load must not be called when fetch fails")
	}
}

func TestGatewayAgent_ReconcileOnce_GenerationFails_ReportsError(t *testing.T) {
	a, pc, cc, gen, _, _ := newTestGatewayAgent(t)
	gen.generateErr = aerrors.New(aerrors.CodeCaddyConfigGenerationFailed, "invalid route")

	if err := a.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cc.loadCalled {
		t.Error("Caddy Load must not be called when generation fails")
	}
	report := pc.lastReport()
	if report == nil || report.Status != "failed" {
		t.Errorf("expected failed report, got %+v", report)
	}
	if report.Error.Code != string(aerrors.CodeCaddyConfigGenerationFailed) {
		t.Errorf("error code: got %q, want CADDY_CONFIG_GENERATION_FAILED", report.Error.Code)
	}
}

func TestGatewayAgent_ReconcileOnce_LoadFails_RestoresLastGoodAndReports(t *testing.T) {
	a, pc, cc, _, _, _ := newTestGatewayAgent(t)
	cc.loadErr = aerrors.New(aerrors.CodeCaddyLoadFailed, "invalid JSON")

	// Seed a last-good config so restore can succeed.
	cc.configs[a.cfg.Caddy.LastGoodConfigPath] = []byte(`{"admin":{}}`)

	if err := a.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	report := pc.lastReport()
	if report == nil || report.Status != "failed" {
		t.Errorf("expected failed report, got %+v", report)
	}
	if report.Error.Code != string(aerrors.CodeCaddyLoadFailed) {
		t.Errorf("error code: got %q, want CADDY_LOAD_FAILED", report.Error.Code)
	}

	// lastSuccessfulVersion must NOT be updated on failure.
	a.mu.Lock()
	lastSuccessful := a.lastSuccessfulVersion
	a.mu.Unlock()
	if lastSuccessful != 0 {
		t.Errorf("lastSuccessfulVersion must stay 0 on failure, got %d", lastSuccessful)
	}
}

func TestGatewayAgent_ReconcileOnce_RouteValidationFails_RestoresAndReports(t *testing.T) {
	a, pc, cc, _, hc, _ := newTestGatewayAgent(t)
	hc.result = &health.Result{Status: health.StatusUnhealthy}
	hc.err = aerrors.New(aerrors.CodeCaddyRouteValidationFailed, "connection refused")

	// Seed a last-good config.
	cc.configs[a.cfg.Caddy.LastGoodConfigPath] = []byte(`{"admin":{}}`)

	// Reset loadErr so /load succeeds (validation fails afterwards).
	cc.loadErr = nil

	if err := a.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	report := pc.lastReport()
	if report == nil || report.Status != "failed" {
		t.Errorf("expected failed report, got %+v", report)
	}
	if report.Error.Code != string(aerrors.CodeCaddyRouteValidationFailed) {
		t.Errorf("error code: got %q, want CADDY_ROUTE_VALIDATION_FAILED", report.Error.Code)
	}
}

// ============================================================
// ValidateRoutes tests
// ============================================================

func TestGatewayAgent_ValidateRoutes_AllHealthy(t *testing.T) {
	a, _, _, _, hc, _ := newTestGatewayAgent(t)
	hc.result = &health.Result{Status: health.StatusHealthy}

	routes := []platform.DesiredRoute{
		{Host: "billing-api.dev.example.com", Upstream: "10.0.2.25:4102", HealthCheckPath: "/health"},
		{Host: "orders-api.dev.example.com", Upstream: "10.0.2.31:4103", HealthCheckPath: "/health"},
	}

	validated, failed := a.validateRoutes(context.Background(), routes)
	if len(failed) != 0 {
		t.Errorf("expected no failures, got %v", failed)
	}
	if validated != 2 {
		t.Errorf("validated: got %d, want 2", validated)
	}
}

func TestGatewayAgent_ValidateRoutes_NoHealthCheckPath_CountedAsOK(t *testing.T) {
	a, _, _, _, _, _ := newTestGatewayAgent(t)

	routes := []platform.DesiredRoute{
		{Host: "worker.dev.example.com", Upstream: "10.0.2.25:4102", HealthCheckPath: ""},
	}

	validated, failed := a.validateRoutes(context.Background(), routes)
	if len(failed) != 0 {
		t.Errorf("expected no failures, got %v", failed)
	}
	if validated != 1 {
		t.Errorf("validated: got %d, want 1", validated)
	}
}

func TestGatewayAgent_ValidateRoutes_OneFailure(t *testing.T) {
	a, _, _, _, hc, _ := newTestGatewayAgent(t)
	hc.result = &health.Result{Status: health.StatusUnhealthy}
	hc.err = aerrors.New(aerrors.CodeHealthCheckFailed, "refused")

	routes := []platform.DesiredRoute{
		{Host: "billing-api.dev.example.com", Upstream: "10.0.2.25:4102", HealthCheckPath: "/health"},
	}

	validated, failed := a.validateRoutes(context.Background(), routes)
	if len(failed) != 1 {
		t.Errorf("expected 1 failure, got %d", len(failed))
	}
	if validated != 0 {
		t.Errorf("validated: got %d, want 0", validated)
	}
}

// ============================================================
// RestoreLastGood tests
// ============================================================

func TestGatewayAgent_RestoreLastGood_NoLastGoodPath_LogsWarning(t *testing.T) {
	a, _, _, _, _, _ := newTestGatewayAgent(t)
	a.cfg.Caddy.LastGoodConfigPath = ""

	// Must not panic.
	a.restoreLastGood(context.Background())
}

func TestGatewayAgent_RestoreLastGood_FileNotFound_LogsWarning(t *testing.T) {
	a, _, _, _, _, _ := newTestGatewayAgent(t)
	// No file seeded in fakeCaddyClient.

	// Must not panic.
	a.restoreLastGood(context.Background())
}

func TestGatewayAgent_RestoreLastGood_LoadsAndAppliesLastGood(t *testing.T) {
	a, _, cc, _, _, _ := newTestGatewayAgent(t)
	lastGood := []byte(`{"admin":{"listen":"0.0.0.0:2019"}}`)
	cc.configs[a.cfg.Caddy.LastGoodConfigPath] = lastGood

	// Reset loadCalled to track the restore Load call.
	cc.loadCalled = false

	a.restoreLastGood(context.Background())

	if !cc.loadCalled {
		t.Error("Load must be called with last-good config during restore")
	}
}

// ============================================================
// ApplyDesiredState: config file saving
// ============================================================

func TestGatewayAgent_ApplyDesiredState_SavesCurrentConfig(t *testing.T) {
	a, _, cc, _, _, _ := newTestGatewayAgent(t)
	ds := &platform.DesiredState{
		Version: 5,
		Type:    "gateway_routes",
		Routes:  []platform.DesiredRoute{},
	}

	_ = a.applyDesiredState(context.Background(), ds)

	if _, ok := cc.configs[a.cfg.Caddy.CurrentConfigPath]; !ok {
		t.Error("current config should be saved after apply")
	}
}

func TestGatewayAgent_ApplyDesiredState_PromotesLastGoodOnSuccess(t *testing.T) {
	a, _, cc, _, _, _ := newTestGatewayAgent(t)
	ds := &platform.DesiredState{
		Version: 5,
		Routes: []platform.DesiredRoute{
			{Host: "billing-api.dev.example.com", Upstream: "10.0.2.25:4102"},
		},
	}

	_ = a.applyDesiredState(context.Background(), ds)

	if _, ok := cc.configs[a.cfg.Caddy.LastGoodConfigPath]; !ok {
		t.Error("last-good config should be promoted after successful apply")
	}
}

func TestGatewayAgent_ApplyDesiredState_DoesNotPromoteLastGoodOnFailure(t *testing.T) {
	a, _, cc, _, _, _ := newTestGatewayAgent(t)
	cc.loadErr = aerrors.New(aerrors.CodeCaddyLoadFailed, "bad config")

	ds := &platform.DesiredState{
		Version: 5,
		Routes:  []platform.DesiredRoute{},
	}

	_ = a.applyDesiredState(context.Background(), ds)

	if _, ok := cc.configs[a.cfg.Caddy.LastGoodConfigPath]; ok {
		t.Error("last-good must not be updated when apply fails")
	}
}
