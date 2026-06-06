package config

import (
	"fmt"
	"os"
	"strings"

	aerrors "devex-agent/internal/errors"

	"gopkg.in/yaml.v3"
)

var validModes = map[string]bool{"runtime": true, "gateway": true}
var validLogLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
var validLogFormats = map[string]bool{"json": true, "text": true}

// Load reads a YAML config file, applies defaults and validates.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: cannot read file %q: %w", path, err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: cannot parse YAML: %w", err)
	}

	applyDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// LoadToken reads the agent token from the file specified in config.
// The token is trimmed of whitespace. It is never logged.
func LoadToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("config: cannot read token file %q: %w", path, err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("config: token file %q is empty", path)
	}
	return token, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Docker.Command == "" {
		cfg.Docker.Command = "docker"
	}
	if cfg.Docker.DefaultRestartPolicy == "" {
		cfg.Docker.DefaultRestartPolicy = "unless-stopped"
	}
	if cfg.Docker.PullTimeoutSecs == 0 {
		cfg.Docker.PullTimeoutSecs = 300
	}
	if cfg.Docker.StartTimeoutSecs == 0 {
		cfg.Docker.StartTimeoutSecs = 60
	}
	if cfg.Docker.StopTimeoutSecs == 0 {
		cfg.Docker.StopTimeoutSecs = 30
	}
	if cfg.Docker.RemoveTimeoutSecs == 0 {
		cfg.Docker.RemoveTimeoutSecs = 30
	}
	if cfg.Docker.InspectTimeoutSecs == 0 {
		cfg.Docker.InspectTimeoutSecs = 10
	}
	if cfg.Docker.ListTimeoutSecs == 0 {
		cfg.Docker.ListTimeoutSecs = 10
	}
	if cfg.HealthCheck.TimeoutSecs == 0 {
		cfg.HealthCheck.TimeoutSecs = 2
	}
	if cfg.HealthCheck.IntervalSecs == 0 {
		cfg.HealthCheck.IntervalSecs = 5
	}
	if cfg.HealthCheck.Retries == 0 {
		cfg.HealthCheck.Retries = 6
	}
	if cfg.Retry.MaxAttempts == 0 {
		cfg.Retry.MaxAttempts = 3
	}
	if cfg.Retry.InitialIntervalSecs == 0 {
		cfg.Retry.InitialIntervalSecs = 1
	}
	if cfg.Retry.MaxIntervalSecs == 0 {
		cfg.Retry.MaxIntervalSecs = 10
	}
	if cfg.Retry.Multiplier == 0 {
		cfg.Retry.Multiplier = 2.0
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "json"
	}
	if cfg.State.Dir == "" {
		cfg.State.Dir = "/var/lib/devex-agent"
	}
	if cfg.Runtime.CommandPollIntervalSecs == 0 {
		cfg.Runtime.CommandPollIntervalSecs = 10
	}
	if cfg.Runtime.DrainingGracePeriodSecs == 0 {
		cfg.Runtime.DrainingGracePeriodSecs = 300
	}
	if cfg.Reconcile.IntervalSecs == 0 {
		cfg.Reconcile.IntervalSecs = 10
	}
}

func validate(cfg *Config) error {
	// --- agent ---
	if !validModes[cfg.Agent.Mode] {
		return configError("agent.mode is required and must be \"runtime\" or \"gateway\"")
	}
	if cfg.Agent.Environment == "" {
		return configError("agent.environment is required")
	}
	if cfg.Agent.Role == "" {
		return configError("agent.role is required")
	}

	// --- platform ---
	if cfg.Platform.BaseURL == "" {
		return configError("platform.base_url is required")
	}
	if cfg.Platform.TokenFile == "" {
		return configError("platform.token_file is required")
	}

	// --- state ---
	if cfg.State.Dir == "" {
		return configError("state.dir is required")
	}

	// --- logging ---
	if !validLogLevels[cfg.Logging.Level] {
		return configError(fmt.Sprintf("logging.level must be one of: debug, info, warn, error (got %q)", cfg.Logging.Level))
	}
	if !validLogFormats[cfg.Logging.Format] {
		return configError(fmt.Sprintf("logging.format must be one of: json, text (got %q)", cfg.Logging.Format))
	}

	// --- runtime-specific ---
	if cfg.Agent.Mode == "runtime" {
		if err := validateRuntime(cfg); err != nil {
			return err
		}
	}

	// --- gateway-specific ---
	if cfg.Agent.Mode == "gateway" {
		if err := validateGateway(cfg); err != nil {
			return err
		}
	}

	return nil
}

func validateRuntime(cfg *Config) error {
	if cfg.Runtime.MaxActiveContainers <= 0 {
		return configError("runtime.max_active_containers must be greater than 0")
	}
	if cfg.Ports.From == 0 || cfg.Ports.To == 0 {
		return configError("ports.from and ports.to are required for runtime mode")
	}
	if cfg.Ports.From > cfg.Ports.To {
		return configError("ports.from must be less than or equal to ports.to")
	}
	portCount := cfg.Ports.To - cfg.Ports.From + 1
	if cfg.Runtime.MaxActiveContainers > portCount {
		return configError(fmt.Sprintf(
			"runtime.max_active_containers (%d) exceeds available port range (%d ports)",
			cfg.Runtime.MaxActiveContainers, portCount,
		))
	}
	if cfg.Docker.Command == "" {
		return configError("docker.command is required for runtime mode")
	}
	return nil
}

func validateGateway(cfg *Config) error {
	if cfg.Caddy.AdminURL == "" {
		return configError("caddy.admin_url is required for gateway mode")
	}
	if !strings.HasPrefix(cfg.Caddy.AdminURL, "http://127.0.0.1") &&
		!strings.HasPrefix(cfg.Caddy.AdminURL, "http://localhost") {
		return configError("caddy.admin_url must point to localhost (127.0.0.1 or localhost)")
	}
	if cfg.Caddy.CurrentConfigPath == "" {
		return configError("caddy.current_config_path is required for gateway mode")
	}
	if cfg.Caddy.PreviousConfigPath == "" {
		return configError("caddy.previous_config_path is required for gateway mode")
	}
	if cfg.Caddy.LastGoodConfigPath == "" {
		return configError("caddy.last_good_config_path is required for gateway mode")
	}
	return nil
}

func configError(msg string) error {
	return aerrors.New(aerrors.CodeConfigInvalid, msg)
}
