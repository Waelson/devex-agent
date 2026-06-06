package caddy_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"devex-agent/internal/caddy"
)

// skipUnlessCaddyIntegration skips the test unless RUN_CADDY_INTEGRATION_TESTS=true.
// Requires Caddy to be running with Admin API on http://127.0.0.1:2019.
func skipUnlessCaddyIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("RUN_CADDY_INTEGRATION_TESTS") != "true" {
		t.Skip("skipping Caddy integration test: set RUN_CADDY_INTEGRATION_TESTS=true to run")
	}
}

const caddyAdminURL = "http://127.0.0.1:2019"

func integrationCaddyClient(t *testing.T) *caddy.Client {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return caddy.NewClient(caddyAdminURL, logger)
}

// fetchCurrentCaddyConfig reads the live Caddy config via GET /config/.
// Returns nil if Caddy reports no config loaded (null response).
func fetchCurrentCaddyConfig(t *testing.T) []byte {
	t.Helper()
	resp, err := http.Get(caddyAdminURL + "/config/")
	if err != nil {
		t.Fatalf("GET /config/ failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /config/ body: %v", err)
	}
	body = bytes.TrimSpace(body)
	if bytes.Equal(body, []byte("null")) {
		return nil
	}
	return body
}

// minimalCaddyConfig returns a minimal valid caddy.json with no routes.
func minimalCaddyConfig() []byte {
	return []byte(`{
		"admin": {"listen": "0.0.0.0:2019"},
		"apps": {
			"http": {
				"servers": {
					"devex-integration-test": {
						"listen": [":17080"],
						"routes": []
					}
				}
			}
		}
	}`)
}

// ─── Tests ───────────────────────────────────────────────────────────────────

func TestIntegration_Caddy_PingRunningInstance(t *testing.T) {
	skipUnlessCaddyIntegration(t)

	client := integrationCaddyClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestIntegration_Caddy_LoadMinimalConfig(t *testing.T) {
	skipUnlessCaddyIntegration(t)

	client := integrationCaddyClient(t)

	// Save current config and restore it after the test.
	originalConfig := fetchCurrentCaddyConfig(t)
	t.Cleanup(func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if originalConfig != nil {
			if err := client.Load(restoreCtx, originalConfig); err != nil {
				t.Logf("WARNING: failed to restore original Caddy config: %v", err)
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := client.Load(ctx, minimalCaddyConfig()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Caddy must still be reachable after the load.
	pingCtx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	if err := client.Ping(pingCtx); err != nil {
		t.Errorf("Ping after Load: %v", err)
	}
}

func TestIntegration_Caddy_LoadInvalidConfig(t *testing.T) {
	skipUnlessCaddyIntegration(t)

	client := integrationCaddyClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := client.Load(ctx, []byte(`this is not valid json {{{`))
	if err == nil {
		t.Fatal("Load with invalid JSON should return an error")
	}
}

func TestIntegration_Caddy_SaveAndRestoreConfig(t *testing.T) {
	skipUnlessCaddyIntegration(t)

	client := integrationCaddyClient(t)
	dir := t.TempDir()

	currentConfigPath := dir + "/current.json"
	previousConfigPath := dir + "/previous.json"

	originalConfig := fetchCurrentCaddyConfig(t)
	t.Cleanup(func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if originalConfig != nil {
			if err := client.Load(restoreCtx, originalConfig); err != nil {
				t.Logf("WARNING: failed to restore original Caddy config: %v", err)
			}
		}
	})

	// Apply test config and save it as "current".
	applyCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	testConfig := minimalCaddyConfig()
	if err := client.Load(applyCtx, testConfig); err != nil {
		t.Fatalf("Load test config: %v", err)
	}
	if err := client.SaveConfig(currentConfigPath, testConfig); err != nil {
		t.Fatalf("SaveConfig current: %v", err)
	}

	// Promote current to previous.
	saved, err := client.LoadConfig(currentConfigPath)
	if err != nil {
		t.Fatalf("LoadConfig current: %v", err)
	}
	if err := client.SaveConfig(previousConfigPath, saved); err != nil {
		t.Fatalf("SaveConfig previous: %v", err)
	}

	// Verify both files are non-empty and identical.
	if len(saved) == 0 {
		t.Error("saved config must not be empty")
	}
	previous, err := client.LoadConfig(previousConfigPath)
	if err != nil {
		t.Fatalf("LoadConfig previous: %v", err)
	}
	if !bytes.Equal(saved, previous) {
		t.Error("current and previous config files should be identical after copy")
	}

	// Reload from previous and verify Caddy accepts it.
	reloadCtx, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()

	if err := client.Load(reloadCtx, previous); err != nil {
		t.Fatalf("Load from previous: %v", err)
	}

	pingCtx, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()

	if err := client.Ping(pingCtx); err != nil {
		t.Errorf("Ping after reload from previous: %v", err)
	}
}

func TestIntegration_Caddy_LoadConfigNotFound(t *testing.T) {
	skipUnlessCaddyIntegration(t)

	client := integrationCaddyClient(t)

	data, err := client.LoadConfig("/tmp/devex-nonexistent-config-xyz.json")
	if err != nil {
		t.Fatalf("LoadConfig on missing file should return nil, nil; got err: %v", err)
	}
	if data != nil {
		t.Errorf("LoadConfig on missing file should return nil data; got %d bytes", len(data))
	}
}
