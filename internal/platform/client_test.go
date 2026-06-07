package platform

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aerrors "devex-agent/internal/errors"
	"log/slog"
)

// --- test helpers ---

// zeroRetry is a retryPolicy with no wait between attempts — keeps tests fast.
var zeroRetry = retryPolicy{
	maxAttempts:     3,
	initialInterval: 0,
	maxInterval:     0,
	multiplier:      1.0,
	jitter:          false,
}

func newTestClient(baseURL string) *Client {
	c := NewClient(baseURL, "test-token-secret", slog.Default())
	c.retry = zeroRetry
	return c
}

func newTestClientWithID(baseURL, agentID string) *Client {
	c := newTestClient(baseURL)
	c.SetAgentID(agentID)
	return c
}

// jsonResponse writes a JSON body with the given status code.
func jsonResponse(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// errorResponse writes a structured Platform API error response.
func errorResponse(w http.ResponseWriter, status int, code, message string, retryable bool) {
	jsonResponse(w, status, map[string]any{
		"error": map[string]any{
			"code":      code,
			"message":   message,
			"retryable": retryable,
		},
	})
}

// failThenSucceed returns a handler that fails for the first n requests with the
// given status, then succeeds with successBody on subsequent requests.
func failThenSucceed(t *testing.T, failStatus, failCount int, successStatus int, successBody any) (http.HandlerFunc, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	return func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if int(n) <= failCount {
			errorResponse(w, failStatus, "SERVER_ERROR", "temporary error", true)
			return
		}
		jsonResponse(w, successStatus, successBody)
	}, &calls
}

// --- Register ---

func TestRegister_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: got %q, want POST", r.Method)
		}
		if r.URL.Path != "/api/agents/register" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		jsonResponse(w, http.StatusCreated, RegisterResponse{
			AgentID: "agent-dev-api-001",
			Status:  "registered",
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	resp, err := c.Register(context.Background(), RegisterRequest{
		Mode:        "runtime",
		Environment: "dev",
		Role:        "api",
		Hostname:    "host-1",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if resp.AgentID != "agent-dev-api-001" {
		t.Errorf("AgentID: got %q", resp.AgentID)
	}
	if resp.Status != "registered" {
		t.Errorf("Status: got %q", resp.Status)
	}
}

func TestRegister_RetriesOn5xx(t *testing.T) {
	handler, calls := failThenSucceed(t, http.StatusInternalServerError, 2, http.StatusCreated,
		RegisterResponse{AgentID: "agent-001", Status: "registered"})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	c := newTestClient(srv.URL)
	resp, err := c.Register(context.Background(), RegisterRequest{Mode: "runtime", Environment: "dev"})
	if err != nil {
		t.Fatalf("Register after retries: %v", err)
	}
	if resp.AgentID != "agent-001" {
		t.Errorf("AgentID: got %q", resp.AgentID)
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", calls.Load())
	}
}

func TestRegister_NoRetryOn401(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		errorResponse(w, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "invalid token", false)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.Register(context.Background(), RegisterRequest{})
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if aerrors.CodeOf(err) != aerrors.CodeAuthenticationFailed {
		t.Errorf("error code: got %q, want AUTHENTICATION_FAILED", aerrors.CodeOf(err))
	}
	if calls.Load() != 1 {
		t.Errorf("expected exactly 1 call for non-retryable 401, got %d", calls.Load())
	}
}

func TestRegister_ExhaustsRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		errorResponse(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "down", true)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	c.retry.maxAttempts = 3
	_, err := c.Register(context.Background(), RegisterRequest{})
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", calls.Load())
	}
}

// --- Auth header ---

func TestClient_SetsAuthorizationHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		jsonResponse(w, http.StatusCreated, RegisterResponse{AgentID: "x", Status: "registered"})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, _ = c.Register(context.Background(), RegisterRequest{})

	if gotAuth != "Bearer test-token-secret" {
		t.Errorf("Authorization header: got %q, want %q", gotAuth, "Bearer test-token-secret")
	}
}

func TestClient_TokenNotInRequestBody(t *testing.T) {
	var reqBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqBody = string(b)
		jsonResponse(w, http.StatusCreated, RegisterResponse{AgentID: "x", Status: "registered"})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, _ = c.Register(context.Background(), RegisterRequest{Mode: "runtime"})

	if strings.Contains(reqBody, "test-token-secret") {
		t.Error("token must not appear in request body")
	}
}

// --- SendHeartbeat ---

func TestSendHeartbeat_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/agent-001/heartbeat" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		jsonResponse(w, http.StatusOK, HeartbeatResponse{
			Status:     "accepted",
			ServerTime: now,
		})
	}))
	defer srv.Close()

	c := newTestClientWithID(srv.URL, "agent-001")
	resp, err := c.SendHeartbeat(context.Background(), HeartbeatRequest{
		Status:      "online",
		Mode:        "runtime",
		Environment: "dev",
		Role:        "api",
		Version:     "0.1.0",
	})
	if err != nil {
		t.Fatalf("SendHeartbeat: %v", err)
	}
	if resp.Status != "accepted" {
		t.Errorf("Status: got %q", resp.Status)
	}
}

func TestSendHeartbeat_RetriesOnServiceUnavailable(t *testing.T) {
	handler, calls := failThenSucceed(t, http.StatusServiceUnavailable, 1, http.StatusOK,
		HeartbeatResponse{Status: "accepted", ServerTime: time.Now()})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	c := newTestClientWithID(srv.URL, "agent-001")
	_, err := c.SendHeartbeat(context.Background(), HeartbeatRequest{Status: "online"})
	if err != nil {
		t.Fatalf("SendHeartbeat after retry: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 calls (1 failure + 1 success), got %d", calls.Load())
	}
}

// --- FetchPendingCommands ---

func TestFetchPendingCommands_Success(t *testing.T) {
	payload := json.RawMessage(`{"application":"billing-api","image":"ghcr.io/x/billing:v1"}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method: got %q, want GET", r.Method)
		}
		jsonResponse(w, http.StatusOK, []PendingCommand{
			{
				ID:            "cmd_123",
				Type:          CommandTypeDeployApplication,
				DeploymentID:  "dep_456",
				TargetAgentID: "agent-001",
				Status:        "pending",
				TimeoutSecs:   600,
				CreatedAt:     time.Now(),
				Payload:       payload,
			},
		})
	}))
	defer srv.Close()

	c := newTestClientWithID(srv.URL, "agent-001")
	cmds, err := c.FetchPendingCommands(context.Background())
	if err != nil {
		t.Fatalf("FetchPendingCommands: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	cmd := cmds[0]
	if cmd.ID != "cmd_123" {
		t.Errorf("ID: got %q", cmd.ID)
	}
	if cmd.Type != CommandTypeDeployApplication {
		t.Errorf("Type: got %q", cmd.Type)
	}
}

func TestFetchPendingCommands_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, []PendingCommand{})
	}))
	defer srv.Close()

	c := newTestClientWithID(srv.URL, "agent-001")
	cmds, err := c.FetchPendingCommands(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmds) != 0 {
		t.Errorf("expected 0 commands, got %d", len(cmds))
	}
}

// --- ClaimCommand ---

func TestClaimCommand_Success(t *testing.T) {
	claimedAt := time.Now().UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/agent-001/commands/cmd_123/claim" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		jsonResponse(w, http.StatusOK, ClaimResponse{
			ID:        "cmd_123",
			Status:    "claimed",
			ClaimedBy: "agent-001",
			ClaimedAt: claimedAt,
		})
	}))
	defer srv.Close()

	c := newTestClientWithID(srv.URL, "agent-001")
	resp, err := c.ClaimCommand(context.Background(), "cmd_123")
	if err != nil {
		t.Fatalf("ClaimCommand: %v", err)
	}
	if resp.Status != "claimed" {
		t.Errorf("Status: got %q", resp.Status)
	}
	if resp.ClaimedBy != "agent-001" {
		t.Errorf("ClaimedBy: got %q", resp.ClaimedBy)
	}
}

func TestClaimCommand_Conflict409_NotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		errorResponse(w, http.StatusConflict, "COMMAND_ALREADY_CLAIMED", "Command is no longer pending", false)
	}))
	defer srv.Close()

	c := newTestClientWithID(srv.URL, "agent-001")
	_, err := c.ClaimCommand(context.Background(), "cmd_123")
	if err == nil {
		t.Fatal("expected error for 409, got nil")
	}
	if aerrors.CodeOf(err) != aerrors.CodeCommandClaimFailed {
		t.Errorf("error code: got %q, want COMMAND_CLAIM_FAILED", aerrors.CodeOf(err))
	}
	if calls.Load() != 1 {
		t.Errorf("409 must not be retried, got %d calls", calls.Load())
	}
}

func TestClaimCommand_Forbidden403_NotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		errorResponse(w, http.StatusForbidden, "AUTHORIZATION_FAILED", "forbidden", false)
	}))
	defer srv.Close()

	c := newTestClientWithID(srv.URL, "agent-001")
	_, err := c.ClaimCommand(context.Background(), "cmd_123")
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if aerrors.CodeOf(err) != aerrors.CodeAuthorizationFailed {
		t.Errorf("error code: got %q", aerrors.CodeOf(err))
	}
	if calls.Load() != 1 {
		t.Errorf("403 must not be retried, got %d calls", calls.Load())
	}
}

// --- diagnostic logging on API errors ---

// recordingHandler is a minimal slog.Handler that captures emitted records for assertions.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) find(message string) (slog.Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message == message {
			return r, true
		}
	}
	return slog.Record{}, false
}

func attrString(t *testing.T, r slog.Record, key string) string {
	t.Helper()
	var found string
	var ok bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = a.Value.String()
			ok = true
			return false
		}
		return true
	})
	if !ok {
		t.Fatalf("attribute %q not found in log record %q", key, r.Message)
	}
	return found
}

func TestClient_LogsDiagnosticsOnAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errorResponse(w, http.StatusConflict, "INVALID_VERSION", "reported version is ahead of current", false)
	}))
	defer srv.Close()

	handler := &recordingHandler{}
	logger := slog.New(handler)

	c := NewClient(srv.URL, "test-token-secret", logger)
	c.retry = zeroRetry
	c.SetAgentID("agent-001")

	err := c.ReportDesiredState(context.Background(), DesiredStateReportRequest{
		Status:              "applied",
		DesiredStateVersion: 1,
		Type:                "gateway_routes",
		Environment:         "dev",
	})
	if err == nil {
		t.Fatal("expected error for 409 response")
	}

	rec, ok := handler.find("platform API request failed")
	if !ok {
		t.Fatal("expected a 'platform API request failed' log record")
	}

	if got := attrString(t, rec, "method"); got != http.MethodPost {
		t.Errorf("method: got %q, want POST", got)
	}
	if got := attrString(t, rec, "path"); got != "/api/agents/agent-001/desired-state/report" {
		t.Errorf("path: got %q", got)
	}
	if got := attrString(t, rec, "status_code"); got != "409" {
		t.Errorf("status_code: got %q, want 409", got)
	}
	if got := attrString(t, rec, "response_body"); !strings.Contains(got, "reported version is ahead of current") {
		t.Errorf("response_body: got %q, want it to contain the platform error message", got)
	}
	if got := attrString(t, rec, "error"); !strings.Contains(got, "reported version is ahead of current") {
		t.Errorf("error: got %q, want it to contain the platform error message", got)
	}

	// The auth token must never appear in diagnostic logs.
	for _, r := range handler.records {
		var attrsDump strings.Builder
		r.Attrs(func(a slog.Attr) bool {
			attrsDump.WriteString(a.Value.String())
			return true
		})
		if strings.Contains(attrsDump.String(), "test-token-secret") {
			t.Errorf("log record %q leaked the auth token: %s", r.Message, attrsDump.String())
		}
	}
}

// --- StartCommand ---

func TestStartCommand_Success(t *testing.T) {
	startedAt := time.Now().UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/agent-001/commands/cmd_123/start" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		jsonResponse(w, http.StatusOK, StartResponse{
			ID:        "cmd_123",
			Status:    "running",
			StartedAt: startedAt,
		})
	}))
	defer srv.Close()

	c := newTestClientWithID(srv.URL, "agent-001")
	resp, err := c.StartCommand(context.Background(), "cmd_123")
	if err != nil {
		t.Fatalf("StartCommand: %v", err)
	}
	if resp.Status != "running" {
		t.Errorf("Status: got %q", resp.Status)
	}
}

// --- ReportCommand ---

func TestReportCommand_Succeeded(t *testing.T) {
	var gotBody CommandReportRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/agent-001/commands/cmd_123/report" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		jsonResponse(w, http.StatusOK, map[string]string{"status": "accepted"})
	}))
	defer srv.Close()

	c := newTestClientWithID(srv.URL, "agent-001")
	err := c.ReportCommand(context.Background(), "cmd_123", CommandReportRequest{
		Status:       "succeeded",
		DeploymentID: "dep_456",
		Result: &CommandResult{
			ContainerName:    "billing-api-dev-v42",
			RuntimePrivateIP: "10.0.2.25",
			HostPort:         4102,
			Health:           "healthy",
			RequiresRoute:    true,
		},
	})
	if err != nil {
		t.Fatalf("ReportCommand: %v", err)
	}
	if gotBody.Status != "succeeded" {
		t.Errorf("reported status: got %q", gotBody.Status)
	}
	if gotBody.Result == nil || gotBody.Result.HostPort != 4102 {
		t.Error("result not sent correctly")
	}
}

func TestReportCommand_Failed(t *testing.T) {
	var gotBody CommandReportRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		jsonResponse(w, http.StatusOK, map[string]string{"status": "accepted"})
	}))
	defer srv.Close()

	c := newTestClientWithID(srv.URL, "agent-001")
	err := c.ReportCommand(context.Background(), "cmd_123", CommandReportRequest{
		Status:       "failed",
		DeploymentID: "dep_456",
		Error: &CommandErrorPayload{
			Code:      "HEALTH_CHECK_FAILED",
			Message:   "no response from /health",
			Operation: "health.http",
			Retryable: false,
		},
	})
	if err != nil {
		t.Fatalf("ReportCommand: %v", err)
	}
	if gotBody.Status != "failed" {
		t.Errorf("reported status: got %q", gotBody.Status)
	}
	if gotBody.Error == nil || gotBody.Error.Code != "HEALTH_CHECK_FAILED" {
		t.Error("error payload not sent correctly")
	}
}

func TestReportCommand_RetriesOn503(t *testing.T) {
	handler, calls := failThenSucceed(t, http.StatusServiceUnavailable, 2, http.StatusOK,
		map[string]string{"status": "accepted"})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	c := newTestClientWithID(srv.URL, "agent-001")
	err := c.ReportCommand(context.Background(), "cmd_123", CommandReportRequest{Status: "succeeded"})
	if err != nil {
		t.Fatalf("ReportCommand after retries: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", calls.Load())
	}
}

// --- FetchDesiredState ---

func TestFetchDesiredState_GatewayRoutes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/agent-gw-001/desired-state" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		jsonResponse(w, http.StatusOK, DesiredState{
			Version:     43,
			Type:        "gateway_routes",
			Environment: "dev",
			Routes: []DesiredRoute{
				{
					ID:              "route_001",
					Host:            "billing-api.dev.example.app",
					Path:            "/",
					Upstream:        "10.0.2.25:4102",
					DeploymentID:    "dep_456",
					HealthCheckPath: "/health",
				},
			},
		})
	}))
	defer srv.Close()

	c := newTestClientWithID(srv.URL, "agent-gw-001")
	state, err := c.FetchDesiredState(context.Background())
	if err != nil {
		t.Fatalf("FetchDesiredState: %v", err)
	}
	if state.Version != 43 {
		t.Errorf("Version: got %d", state.Version)
	}
	if state.Type != "gateway_routes" {
		t.Errorf("Type: got %q", state.Type)
	}
	if len(state.Routes) != 1 {
		t.Fatalf("Routes: got %d, want 1", len(state.Routes))
	}
	if state.Routes[0].Upstream != "10.0.2.25:4102" {
		t.Errorf("Upstream: got %q", state.Routes[0].Upstream)
	}
}

func TestFetchDesiredState_RuntimeDeployments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, DesiredState{
			Version:     12,
			Type:        "runtime_deployments",
			Environment: "dev",
			Deployments: []DesiredDeployment{
				{
					DeploymentID:          "dep_456",
					Application:           "billing-api",
					ContainerName:         "billing-api-dev-v42",
					Image:                 "ghcr.io/x/billing:v42",
					HostPort:              4102,
					ContainerInternalPort: 3000,
					Status:                "active",
				},
			},
		})
	}))
	defer srv.Close()

	c := newTestClientWithID(srv.URL, "agent-001")
	state, err := c.FetchDesiredState(context.Background())
	if err != nil {
		t.Fatalf("FetchDesiredState: %v", err)
	}
	if len(state.Deployments) != 1 {
		t.Fatalf("Deployments: got %d, want 1", len(state.Deployments))
	}
	if state.Deployments[0].HostPort != 4102 {
		t.Errorf("HostPort: got %d", state.Deployments[0].HostPort)
	}
}

// --- ReportDesiredState ---

func TestReportDesiredState_Applied(t *testing.T) {
	var gotBody DesiredStateReportRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/agent-gw-001/desired-state/report" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		jsonResponse(w, http.StatusOK, map[string]string{"status": "accepted"})
	}))
	defer srv.Close()

	now := time.Now().UTC()
	c := newTestClientWithID(srv.URL, "agent-gw-001")
	err := c.ReportDesiredState(context.Background(), DesiredStateReportRequest{
		Status:              "applied",
		DesiredStateVersion: 43,
		Type:                "gateway_routes",
		RoutesTotal:         12,
		ValidatedRoutes:     12,
		FailedRoutes:        0,
		AppliedAt:           &now,
	})
	if err != nil {
		t.Fatalf("ReportDesiredState: %v", err)
	}
	if gotBody.Status != "applied" {
		t.Errorf("status: got %q", gotBody.Status)
	}
	if gotBody.DesiredStateVersion != 43 {
		t.Errorf("desired_state_version: got %d", gotBody.DesiredStateVersion)
	}
}

func TestReportDesiredState_Failed(t *testing.T) {
	var gotBody DesiredStateReportRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		jsonResponse(w, http.StatusOK, map[string]string{"status": "accepted"})
	}))
	defer srv.Close()

	c := newTestClientWithID(srv.URL, "agent-gw-001")
	err := c.ReportDesiredState(context.Background(), DesiredStateReportRequest{
		Status:              "failed",
		DesiredStateVersion: 43,
		Type:                "gateway_routes",
		Error: &CommandErrorPayload{
			Code:      "CADDY_LOAD_FAILED",
			Message:   "caddy rejected config",
			Operation: "caddy.load",
		},
	})
	if err != nil {
		t.Fatalf("ReportDesiredState: %v", err)
	}
	if gotBody.Error == nil || gotBody.Error.Code != "CADDY_LOAD_FAILED" {
		t.Error("error payload not sent correctly")
	}
}

// --- context cancellation ---

func TestClient_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow server.
		time.Sleep(200 * time.Millisecond)
		jsonResponse(w, http.StatusOK, RegisterResponse{AgentID: "x"})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	c := newTestClient(srv.URL)
	c.retry.maxAttempts = 1 // no retries so we don't confuse the timeout assertion
	_, err := c.Register(ctx, RegisterRequest{})
	if err == nil {
		t.Fatal("expected error from context timeout, got nil")
	}
}

// --- capabilities helpers ---

func TestNewRuntimeCapabilities(t *testing.T) {
	caps := NewRuntimeCapabilities([]string{"api"}, 10, 4100, 4114)

	if caps["max_active_containers"] != 10 {
		t.Errorf("max_active_containers: got %v", caps["max_active_containers"])
	}
	pr, ok := caps["port_range"].(map[string]any)
	if !ok {
		t.Fatal("port_range not a map")
	}
	if pr["from"] != 4100 || pr["to"] != 4114 {
		t.Errorf("port_range: got %v", pr)
	}
}

func TestNewGatewayCapabilities(t *testing.T) {
	caps := NewGatewayCapabilities("http://127.0.0.1:2019")

	if caps["gateway"] != true {
		t.Errorf("gateway: got %v", caps["gateway"])
	}
	if caps["caddy_admin_url"] != "http://127.0.0.1:2019" {
		t.Errorf("caddy_admin_url: got %v", caps["caddy_admin_url"])
	}
}

// --- retryableStatus helper ---

func TestRetryableStatus(t *testing.T) {
	retryable := []int{0, 408, 429, 500, 502, 503, 504}
	for _, s := range retryable {
		if !retryableStatus(s) {
			t.Errorf("status %d should be retryable", s)
		}
	}

	notRetryable := []int{200, 201, 400, 401, 403, 404, 409, 422}
	for _, s := range notRetryable {
		if retryableStatus(s) {
			t.Errorf("status %d should NOT be retryable", s)
		}
	}
}
