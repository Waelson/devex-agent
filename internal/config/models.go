package config

// Config is the top-level configuration for the devex-agent.
type Config struct {
	Agent       AgentConfig       `yaml:"agent"`
	Platform    PlatformConfig    `yaml:"platform"`
	Runtime     RuntimeConfig     `yaml:"runtime"`
	Ports       PortsConfig       `yaml:"ports"`
	Docker      DockerConfig      `yaml:"docker"`
	HealthCheck HealthCheckConfig `yaml:"health_check"`
	Retry       RetryConfig       `yaml:"retry"`
	Caddy       CaddyConfig       `yaml:"caddy"`
	Reconcile   ReconcileConfig   `yaml:"reconcile"`
	State       StateConfig       `yaml:"state"`
	Logging     LoggingConfig     `yaml:"logging"`
}

type AgentConfig struct {
	ID          string `yaml:"id"`
	Mode        string `yaml:"mode"`
	Environment string `yaml:"environment"`
	Role        string `yaml:"role"`
}

type PlatformConfig struct {
	BaseURL   string `yaml:"base_url"`
	TokenFile string `yaml:"token_file"`
}

type RuntimeConfig struct {
	MaxActiveContainers       int `yaml:"max_active_containers"`
	DrainingGracePeriodSecs   int `yaml:"draining_grace_period_seconds"`
	CommandPollIntervalSecs   int `yaml:"command_poll_interval_seconds"`
}

type PortsConfig struct {
	From int `yaml:"from"`
	To   int `yaml:"to"`
}

type DockerConfig struct {
	Command              string `yaml:"command"`
	PullTimeoutSecs      int    `yaml:"pull_timeout_seconds"`
	StartTimeoutSecs     int    `yaml:"start_timeout_seconds"`
	StopTimeoutSecs      int    `yaml:"stop_timeout_seconds"`
	RemoveTimeoutSecs    int    `yaml:"remove_timeout_seconds"`
	InspectTimeoutSecs   int    `yaml:"inspect_timeout_seconds"`
	ListTimeoutSecs      int    `yaml:"list_timeout_seconds"`
	DefaultRestartPolicy string `yaml:"default_restart_policy"`
}

type HealthCheckConfig struct {
	TimeoutSecs  int `yaml:"timeout_seconds"`
	IntervalSecs int `yaml:"interval_seconds"`
	Retries      int `yaml:"retries"`
}

type RetryConfig struct {
	MaxAttempts         int     `yaml:"max_attempts"`
	InitialIntervalSecs int     `yaml:"initial_interval_seconds"`
	MaxIntervalSecs     int     `yaml:"max_interval_seconds"`
	Multiplier          float64 `yaml:"multiplier"`
	Jitter              bool    `yaml:"jitter"`
}

type CaddyConfig struct {
	AdminURL                   string `yaml:"admin_url"`
	CurrentConfigPath          string `yaml:"current_config_path"`
	PreviousConfigPath         string `yaml:"previous_config_path"`
	LastGoodConfigPath         string `yaml:"last_good_config_path"`
	LoadTimeoutSecs            int    `yaml:"load_timeout_seconds"`
	RouteValidationTimeoutSecs int    `yaml:"route_validation_timeout_seconds"`
}

type ReconcileConfig struct {
	IntervalSecs int `yaml:"interval_seconds"`
}

type StateConfig struct {
	Dir string `yaml:"dir"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}
