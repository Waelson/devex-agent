package caddy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// ============================================================
// Load
// ============================================================

func TestLoad_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/load" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type: got %q", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newClientWithHTTP(srv.URL, srv.Client(), nopLogger())
	err := c.Load(context.Background(), []byte(`{"admin":{}}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoad_NonSuccess_ReturnsCaddyLoadFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid config"))
	}))
	defer srv.Close()

	c := newClientWithHTTP(srv.URL, srv.Client(), nopLogger())
	err := c.Load(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !containsCode(err.Error(), "CADDY_LOAD_FAILED") {
		t.Errorf("error should contain CADDY_LOAD_FAILED, got: %v", err)
	}
}

func TestLoad_ServerError_ReturnsCaddyLoadFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newClientWithHTTP(srv.URL, srv.Client(), nopLogger())
	err := c.Load(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !containsCode(err.Error(), "CADDY_LOAD_FAILED") {
		t.Errorf("error should contain CADDY_LOAD_FAILED, got: %v", err)
	}
}

func TestLoad_ConnectionRefused_ReturnsCaddyAdminUnavailable(t *testing.T) {
	// Port 1 is reserved and should refuse connections.
	c := newClientWithHTTP("http://127.0.0.1:1", http.DefaultClient, nopLogger())
	err := c.Load(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("expected connection refused error")
	}
	if !containsCode(err.Error(), "CADDY_ADMIN_UNAVAILABLE") {
		t.Errorf("error should contain CADDY_ADMIN_UNAVAILABLE, got: %v", err)
	}
}

func TestLoad_ContextCancelled_ReturnsCaddyAdminUnavailable(t *testing.T) {
	// Server that never responds.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		// hang
		select {}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	c := newClientWithHTTP(srv.URL, srv.Client(), nopLogger())
	err := c.Load(ctx, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ============================================================
// Ping
// ============================================================

func TestPing_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/config" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newClientWithHTTP(srv.URL, srv.Client(), nopLogger())
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPing_4xx_IsConsideredReachable(t *testing.T) {
	// 404 means admin API is reachable even if no config exists yet.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newClientWithHTTP(srv.URL, srv.Client(), nopLogger())
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: got error for 404 (should be OK): %v", err)
	}
}

func TestPing_5xx_ReturnsCaddyAdminUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newClientWithHTTP(srv.URL, srv.Client(), nopLogger())
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !containsCode(err.Error(), "CADDY_ADMIN_UNAVAILABLE") {
		t.Errorf("error should contain CADDY_ADMIN_UNAVAILABLE, got: %v", err)
	}
}

func TestPing_ConnectionRefused_ReturnsCaddyAdminUnavailable(t *testing.T) {
	c := newClientWithHTTP("http://127.0.0.1:1", http.DefaultClient, nopLogger())
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
	if !containsCode(err.Error(), "CADDY_ADMIN_UNAVAILABLE") {
		t.Errorf("error should contain CADDY_ADMIN_UNAVAILABLE, got: %v", err)
	}
}

// ============================================================
// SaveConfig / LoadConfig
// ============================================================

func TestSaveConfig_WritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "caddy.json")
	data := []byte(`{"admin":{}}`)

	c := newClientWithHTTP("http://127.0.0.1:2019", http.DefaultClient, nopLogger())
	if err := c.SaveConfig(path, data); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("file content: got %q, want %q", got, data)
	}
}

func TestSaveConfig_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c", "caddy.json")

	c := newClientWithHTTP("http://127.0.0.1:2019", http.DefaultClient, nopLogger())
	if err := c.SaveConfig(nested, []byte(`{}`)); err != nil {
		t.Fatalf("SaveConfig with nested dirs: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestSaveConfig_IsAtomic_NoTmpFileLeft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "caddy.json")

	c := newClientWithHTTP("http://127.0.0.1:2019", http.DefaultClient, nopLogger())
	if err := c.SaveConfig(path, []byte(`{}`)); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "caddy.json" {
			t.Errorf("unexpected file left: %s (expected only caddy.json)", e.Name())
		}
	}
}

func TestLoadConfig_ReturnsNilWhenNotFound(t *testing.T) {
	c := newClientWithHTTP("http://127.0.0.1:2019", http.DefaultClient, nopLogger())
	data, err := c.LoadConfig("/no/such/path/caddy.json")
	if err != nil {
		t.Fatalf("LoadConfig on missing file: %v", err)
	}
	if data != nil {
		t.Errorf("expected nil data for missing file, got %q", data)
	}
}

func TestLoadConfig_ReadsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "caddy.json")
	want := []byte(`{"test":true}`)
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	c := newClientWithHTTP("http://127.0.0.1:2019", http.DefaultClient, nopLogger())
	got, err := c.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("LoadConfig: got %q, want %q", got, want)
	}
}

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "caddy.json")
	original := []byte(`{"admin":{"listen":"0.0.0.0:2019"}}`)

	c := newClientWithHTTP("http://127.0.0.1:2019", http.DefaultClient, nopLogger())
	if err := c.SaveConfig(path, original); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	loaded, err := c.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if string(loaded) != string(original) {
		t.Errorf("round-trip mismatch: got %q, want %q", loaded, original)
	}
}

// ============================================================
// helpers
// ============================================================

func containsCode(s, code string) bool {
	return len(s) >= len(code) && (s == code ||
		func() bool {
			for i := 0; i <= len(s)-len(code); i++ {
				if s[i:i+len(code)] == code {
					return true
				}
			}
			return false
		}())
}
