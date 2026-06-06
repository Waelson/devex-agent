package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	aerrors "devex-agent/internal/errors"
)

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	return path
}

func writeToken(t *testing.T, dir, token string) string {
	t.Helper()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte(token), 0600); err != nil {
		t.Fatalf("writeToken: %v", err)
	}
	return path
}

const validRuntimeConfig = `
agent:
  mode: "runtime"
  environment: "dev"
  role: "api"
platform:
  base_url: "https://platform.example.com"
  token_file: "/tmp/token"
runtime:
  max_active_containers: 10
ports:
  from: 4100
  to: 4114
state:
  dir: "/var/lib/devex-agent"
`

const validGatewayConfig = `
agent:
  mode: "gateway"
  environment: "dev"
  role: "gateway"
platform:
  base_url: "https://platform.example.com"
  token_file: "/tmp/token"
caddy:
  admin_url: "http://127.0.0.1:2019"
  current_config_path: "/var/lib/devex-agent/gateway/current-caddy.json"
  previous_config_path: "/var/lib/devex-agent/gateway/previous-caddy.json"
  last_good_config_path: "/var/lib/devex-agent/gateway/last-good-caddy.json"
state:
  dir: "/var/lib/devex-agent"
`

func TestLoad_ValidRuntime(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, validRuntimeConfig)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Agent.Mode != "runtime" {
		t.Errorf("mode: got %q, want %q", cfg.Agent.Mode, "runtime")
	}
	if cfg.Agent.Environment != "dev" {
		t.Errorf("environment: got %q, want %q", cfg.Agent.Environment, "dev")
	}
	if cfg.Ports.From != 4100 || cfg.Ports.To != 4114 {
		t.Errorf("ports: got %d-%d, want 4100-4114", cfg.Ports.From, cfg.Ports.To)
	}
}

func TestLoad_ValidGateway(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, validGatewayConfig)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Agent.Mode != "gateway" {
		t.Errorf("mode: got %q, want %q", cfg.Agent.Mode, "gateway")
	}
	if cfg.Caddy.AdminURL != "http://127.0.0.1:2019" {
		t.Errorf("caddy.admin_url: got %q", cfg.Caddy.AdminURL)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, ":::invalid yaml:::")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoad_MissingMode(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agent:
  environment: "dev"
  role: "api"
platform:
  base_url: "https://platform.example.com"
  token_file: "/tmp/token"
`)
	_, err := Load(path)
	assertConfigInvalid(t, err, "mode")
}

func TestLoad_InvalidMode(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agent:
  mode: "unknown"
  environment: "dev"
  role: "api"
platform:
  base_url: "https://platform.example.com"
  token_file: "/tmp/token"
`)
	_, err := Load(path)
	assertConfigInvalid(t, err, "mode")
}

func TestLoad_MissingEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agent:
  mode: "runtime"
  role: "api"
platform:
  base_url: "https://platform.example.com"
  token_file: "/tmp/token"
runtime:
  max_active_containers: 5
ports:
  from: 4100
  to: 4114
`)
	_, err := Load(path)
	assertConfigInvalid(t, err, "environment")
}

func TestLoad_MissingPlatformBaseURL(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agent:
  mode: "runtime"
  environment: "dev"
  role: "api"
platform:
  token_file: "/tmp/token"
runtime:
  max_active_containers: 5
ports:
  from: 4100
  to: 4114
`)
	_, err := Load(path)
	assertConfigInvalid(t, err, "base_url")
}

func TestLoad_RuntimePortsFromGreaterThanTo(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agent:
  mode: "runtime"
  environment: "dev"
  role: "api"
platform:
  base_url: "https://platform.example.com"
  token_file: "/tmp/token"
runtime:
  max_active_containers: 5
ports:
  from: 4114
  to: 4100
`)
	_, err := Load(path)
	assertConfigInvalid(t, err, "ports.from")
}

func TestLoad_RuntimeMaxContainersExceedsPortRange(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agent:
  mode: "runtime"
  environment: "dev"
  role: "api"
platform:
  base_url: "https://platform.example.com"
  token_file: "/tmp/token"
runtime:
  max_active_containers: 20
ports:
  from: 4100
  to: 4105
`)
	_, err := Load(path)
	assertConfigInvalid(t, err, "max_active_containers")
}

func TestLoad_GatewayMissingAdminURL(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agent:
  mode: "gateway"
  environment: "dev"
  role: "gateway"
platform:
  base_url: "https://platform.example.com"
  token_file: "/tmp/token"
caddy:
  current_config_path: "/tmp/current.json"
  previous_config_path: "/tmp/previous.json"
  last_good_config_path: "/tmp/last-good.json"
`)
	_, err := Load(path)
	assertConfigInvalid(t, err, "admin_url")
}

func TestLoad_GatewayPublicAdminURL(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
agent:
  mode: "gateway"
  environment: "dev"
  role: "gateway"
platform:
  base_url: "https://platform.example.com"
  token_file: "/tmp/token"
caddy:
  admin_url: "http://0.0.0.0:2019"
  current_config_path: "/tmp/current.json"
  previous_config_path: "/tmp/previous.json"
  last_good_config_path: "/tmp/last-good.json"
`)
	_, err := Load(path)
	assertConfigInvalid(t, err, "localhost")
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, validRuntimeConfig+`
logging:
  level: "verbose"
  format: "json"
`)
	_, err := Load(path)
	assertConfigInvalid(t, err, "logging.level")
}

func TestLoad_DefaultsApplied(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, validRuntimeConfig)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Docker.Command != "docker" {
		t.Errorf("docker.command default: got %q", cfg.Docker.Command)
	}
	if cfg.Docker.DefaultRestartPolicy != "unless-stopped" {
		t.Errorf("docker.restart_policy default: got %q", cfg.Docker.DefaultRestartPolicy)
	}
	if cfg.HealthCheck.Retries != 6 {
		t.Errorf("health_check.retries default: got %d", cfg.HealthCheck.Retries)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("logging.level default: got %q", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "json" {
		t.Errorf("logging.format default: got %q", cfg.Logging.Format)
	}
	if cfg.Retry.Multiplier != 2.0 {
		t.Errorf("retry.multiplier default: got %f", cfg.Retry.Multiplier)
	}
}

func TestLoadToken_Success(t *testing.T) {
	dir := t.TempDir()
	path := writeToken(t, dir, "my-secret-token\n")

	token, err := LoadToken(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "my-secret-token" {
		t.Errorf("token: got %q, want %q", token, "my-secret-token")
	}
}

func TestLoadToken_FileNotFound(t *testing.T) {
	_, err := LoadToken("/nonexistent/token")
	if err == nil {
		t.Fatal("expected error for missing token file, got nil")
	}
}

func TestLoadToken_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := writeToken(t, dir, "   \n  ")

	_, err := LoadToken(path)
	if err == nil {
		t.Fatal("expected error for empty token, got nil")
	}
}

func assertConfigInvalid(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected CONFIG_INVALID error, got nil")
	}
	var opErr *aerrors.OperationalError
	if !aerrors.As(err, &opErr) {
		t.Fatalf("expected *OperationalError, got %T: %v", err, err)
	}
	if opErr.Code != aerrors.CodeConfigInvalid {
		t.Errorf("error code: got %q, want %q", opErr.Code, aerrors.CodeConfigInvalid)
	}
	if contains != "" && !strings.Contains(opErr.Message, contains) {
		t.Errorf("error message %q does not contain %q", opErr.Message, contains)
	}
}
