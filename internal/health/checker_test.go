package health

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	aerrors "devex-agent/internal/errors"
)

// --- test helpers ---

func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newChecker(t *testing.T) *DefaultChecker {
	t.Helper()
	return NewDefaultChecker(nopLogger())
}

// instantConfig returns a CheckConfig that executes immediately (no interval between retries).
func instantConfig(retries int) CheckConfig {
	return CheckConfig{
		TimeoutSeconds:  2,
		IntervalSeconds: 0,
		Retries:         retries,
	}
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

// --- CheckHTTP ---

func TestCheckHTTP_Success200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newChecker(t)
	result, err := c.CheckHTTP(context.Background(), HTTPCheckTarget{
		URL:    srv.URL + "/health",
		Config: instantConfig(1),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Healthy() {
		t.Errorf("expected healthy, got %+v", result)
	}
	if result.StatusCode != http.StatusOK {
		t.Errorf("status code: got %d", result.StatusCode)
	}
	if result.Type != TypeHTTP {
		t.Errorf("type: got %q", result.Type)
	}
	if result.Attempts != 1 {
		t.Errorf("attempts: got %d", result.Attempts)
	}
	if result.Duration <= 0 {
		t.Error("duration must be > 0")
	}
}

func TestCheckHTTP_Success204(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newChecker(t)
	result, err := c.CheckHTTP(context.Background(), HTTPCheckTarget{
		URL:    srv.URL + "/health",
		Config: instantConfig(1),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Healthy() {
		t.Errorf("expected healthy")
	}
}

func TestCheckHTTP_UnexpectedStatus500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newChecker(t)
	result, err := c.CheckHTTP(context.Background(), HTTPCheckTarget{
		URL:    srv.URL + "/health",
		Config: instantConfig(1),
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	assertErrorCode(t, err, aerrors.CodeHealthCheckUnexpectedStatus)
	if result.Healthy() {
		t.Error("result should be unhealthy")
	}
	if result.StatusCode != http.StatusInternalServerError {
		t.Errorf("status code: got %d", result.StatusCode)
	}
}

func TestCheckHTTP_UnexpectedStatus404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newChecker(t)
	_, err := c.CheckHTTP(context.Background(), HTTPCheckTarget{
		URL:    srv.URL + "/health",
		Config: instantConfig(1),
	})

	assertErrorCode(t, err, aerrors.CodeHealthCheckUnexpectedStatus)
}

func TestCheckHTTP_Retries_SucceedsOnThirdAttempt(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(callCount.Add(1))
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newChecker(t)
	result, err := c.CheckHTTP(context.Background(), HTTPCheckTarget{
		URL:    srv.URL + "/health",
		Config: instantConfig(6),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Healthy() {
		t.Error("expected healthy")
	}
	if result.Attempts != 3 {
		t.Errorf("attempts: got %d, want 3", result.Attempts)
	}
	if int(callCount.Load()) != 3 {
		t.Errorf("server calls: got %d, want 3", callCount.Load())
	}
}

func TestCheckHTTP_AllRetriesExhausted(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newChecker(t)
	result, err := c.CheckHTTP(context.Background(), HTTPCheckTarget{
		URL:    srv.URL + "/health",
		Config: instantConfig(4),
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if result.Healthy() {
		t.Error("result should be unhealthy")
	}
	if result.Attempts != 4 {
		t.Errorf("attempts: got %d, want 4", result.Attempts)
	}
	if int(callCount.Load()) != 4 {
		t.Errorf("server calls: got %d, want 4", callCount.Load())
	}
}

func TestCheckHTTP_ConnectionRefused(t *testing.T) {
	// Use a port that is definitely not listening.
	c := newChecker(t)
	result, err := c.CheckHTTP(context.Background(), HTTPCheckTarget{
		URL:    "http://127.0.0.1:19999/health",
		Config: instantConfig(1),
	})

	assertErrorCode(t, err, aerrors.CodeHealthCheckConnectionRefused)
	if result.Healthy() {
		t.Error("result should be unhealthy")
	}
}

func TestCheckHTTP_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until client disconnects.
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	c := newChecker(t)
	result, err := c.CheckHTTP(ctx, HTTPCheckTarget{
		URL:    srv.URL + "/health",
		Config: CheckConfig{TimeoutSeconds: 5, Retries: 1},
	})

	if err == nil {
		t.Fatal("expected error on context cancellation")
	}
	if result.Healthy() {
		t.Error("result should be unhealthy")
	}
}

func TestCheckHTTP_3xxRejectedByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "http://other.example.com/health")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	c := newChecker(t)
	_, err := c.CheckHTTP(context.Background(), HTTPCheckTarget{
		URL:    srv.URL + "/health",
		Config: instantConfig(1),
	})

	// 302 is not 2xx and Allow3xx is false — must fail.
	if err == nil {
		t.Fatal("expected error for 302 without Allow3xx")
	}
	assertErrorCode(t, err, aerrors.CodeHealthCheckUnexpectedStatus)
}

func TestCheckHTTP_3xxAllowedWhenConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "http://other.example.com/health")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	// The client does not follow redirects (ErrUseLastResponse), so we get 302 back.
	c := newChecker(t)
	result, err := c.CheckHTTP(context.Background(), HTTPCheckTarget{
		URL:    srv.URL + "/health",
		Config: CheckConfig{TimeoutSeconds: 2, Retries: 1, Allow3xx: true},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Healthy() {
		t.Error("expected healthy with Allow3xx=true")
	}
}

func TestCheckHTTP_GatewayRoute_SetsHostHeader(t *testing.T) {
	var receivedHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newChecker(t)
	result, err := c.CheckHTTP(context.Background(), HTTPCheckTarget{
		URL:       srv.URL + "/health",
		Host:      "billing-api.dev.useclarus.app",
		CheckType: TypeGatewayRoute,
		Config:    instantConfig(1),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Healthy() {
		t.Error("expected healthy")
	}
	if result.Type != TypeGatewayRoute {
		t.Errorf("type: got %q, want %q", result.Type, TypeGatewayRoute)
	}
	if receivedHost != "billing-api.dev.useclarus.app" {
		t.Errorf("server received Host=%q, want billing-api.dev.useclarus.app", receivedHost)
	}
}

func TestCheckHTTP_ResultContainsErrorCodeOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newChecker(t)
	result, _ := c.CheckHTTP(context.Background(), HTTPCheckTarget{
		URL:    srv.URL + "/health",
		Config: instantConfig(1),
	})

	if result.ErrorCode == "" {
		t.Error("result.ErrorCode must not be empty on failure")
	}
	if result.ErrorMessage == "" {
		t.Error("result.ErrorMessage must not be empty on failure")
	}
}

// --- CheckTCP ---

func TestCheckTCP_Success(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	// Accept connections in background so the dial completes.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	c := newChecker(t)
	result, err := c.CheckTCP(context.Background(), TCPCheckTarget{
		Address: ln.Addr().String(),
		Config:  instantConfig(1),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Healthy() {
		t.Error("expected healthy")
	}
	if result.Type != TypeTCP {
		t.Errorf("type: got %q", result.Type)
	}
}

func TestCheckTCP_ConnectionRefused(t *testing.T) {
	c := newChecker(t)
	result, err := c.CheckTCP(context.Background(), TCPCheckTarget{
		Address: "127.0.0.1:19998",
		Config:  instantConfig(1),
	})

	assertErrorCode(t, err, aerrors.CodeHealthCheckConnectionRefused)
	if result.Healthy() {
		t.Error("result should be unhealthy")
	}
}

func TestCheckTCP_Retries(t *testing.T) {
	// Bind a listener but don't accept — close it immediately to cause ECONNREFUSED.
	// Then start a real listener for the third attempt.
	// Since we can't easily control per-attempt behavior for TCP, we test that
	// multiple attempts are made by verifying the Attempts field.
	c := newChecker(t)
	result, err := c.CheckTCP(context.Background(), TCPCheckTarget{
		Address: "127.0.0.1:19997",
		Config:  instantConfig(3),
	})

	if err == nil {
		t.Fatal("expected error (no listener)")
	}
	if result.Attempts != 3 {
		t.Errorf("attempts: got %d, want 3", result.Attempts)
	}
}

// --- CheckContainer ---

func TestCheckContainer_Running(t *testing.T) {
	c := newChecker(t)
	result, err := c.CheckContainer(context.Background(), ContainerStatus{
		Name:    "billing-api-dev-v42",
		Running: true,
		Status:  "running",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Healthy() {
		t.Error("expected healthy")
	}
	if result.Type != TypeContainer {
		t.Errorf("type: got %q", result.Type)
	}
	if result.Attempts != 1 {
		t.Errorf("attempts: got %d", result.Attempts)
	}
}

func TestCheckContainer_NotRunning(t *testing.T) {
	c := newChecker(t)
	result, err := c.CheckContainer(context.Background(), ContainerStatus{
		Name:    "billing-api-dev-v42",
		Running: false,
		Status:  "created",
	})

	assertErrorCode(t, err, aerrors.CodeHealthCheckContainerNotRunning)
	if result.Healthy() {
		t.Error("result should be unhealthy")
	}
}

func TestCheckContainer_ExitedWithError(t *testing.T) {
	c := newChecker(t)
	result, err := c.CheckContainer(context.Background(), ContainerStatus{
		Name:     "billing-api-dev-v42",
		Running:  false,
		Status:   "exited",
		ExitCode: 1,
	})

	assertErrorCode(t, err, aerrors.CodeHealthCheckContainerNotRunning)
	if result.Healthy() {
		t.Error("result should be unhealthy")
	}
	if result.ErrorMessage == "" {
		t.Error("error message must not be empty")
	}
}

func TestCheckContainer_DeadStatus(t *testing.T) {
	c := newChecker(t)
	_, err := c.CheckContainer(context.Background(), ContainerStatus{
		Name:    "billing-api-dev-v42",
		Running: false,
		Status:  "dead",
	})
	assertErrorCode(t, err, aerrors.CodeHealthCheckContainerNotRunning)
}

func TestCheckContainer_RunningFlagFalseIsUnhealthy(t *testing.T) {
	// Even if Status says "running" but Running=false, it should fail.
	c := newChecker(t)
	_, err := c.CheckContainer(context.Background(), ContainerStatus{
		Name:    "billing-api-dev-v42",
		Running: false,
		Status:  "running", // inconsistent state
	})
	assertErrorCode(t, err, aerrors.CodeHealthCheckContainerNotRunning)
}

func TestCheckContainer_ResultTargetContainerName(t *testing.T) {
	c := newChecker(t)
	result, _ := c.CheckContainer(context.Background(), ContainerStatus{
		Name:    "billing-api-dev-v42",
		Running: true,
		Status:  "running",
	})
	if result.Target != "container://billing-api-dev-v42" {
		t.Errorf("target: got %q", result.Target)
	}
}

// --- applyHTTPDefaults ---

func TestApplyHTTPDefaults_ZeroValuesGetDefaults(t *testing.T) {
	cfg := applyHTTPDefaults(CheckConfig{})
	if cfg.TimeoutSeconds != 2 {
		t.Errorf("TimeoutSeconds: got %d, want 2", cfg.TimeoutSeconds)
	}
	if cfg.Retries != 1 {
		t.Errorf("Retries: got %d, want 1", cfg.Retries)
	}
}

func TestApplyHTTPDefaults_NonZeroValuesPreserved(t *testing.T) {
	cfg := applyHTTPDefaults(CheckConfig{TimeoutSeconds: 5, Retries: 6, IntervalSeconds: 3})
	if cfg.TimeoutSeconds != 5 {
		t.Errorf("TimeoutSeconds: got %d, want 5", cfg.TimeoutSeconds)
	}
	if cfg.Retries != 6 {
		t.Errorf("Retries: got %d, want 6", cfg.Retries)
	}
	if cfg.IntervalSeconds != 3 {
		t.Errorf("IntervalSeconds: got %d, want 3", cfg.IntervalSeconds)
	}
}

// --- Result ---

func TestResult_Healthy(t *testing.T) {
	r := &Result{Status: StatusHealthy}
	if !r.Healthy() {
		t.Error("expected Healthy() = true")
	}

	r2 := &Result{Status: StatusUnhealthy}
	if r2.Healthy() {
		t.Error("expected Healthy() = false for unhealthy result")
	}
}
