package health

import "time"

// Status values for Result.
const (
	StatusHealthy   = "healthy"
	StatusUnhealthy = "unhealthy"
)

// Check types.
const (
	TypeHTTP         = "http"
	TypeTCP          = "tcp"
	TypeContainer    = "container"
	TypeGatewayRoute = "gateway_route"
)

// CheckConfig holds retry and timeout parameters shared across check types.
type CheckConfig struct {
	TimeoutSeconds  int // per-attempt timeout; 0 → defaults to 2s
	IntervalSeconds int // wait between attempts; 0 → no wait (useful in tests)
	Retries         int // total attempts = Retries; 0 or 1 means try once
	Allow3xx        bool
}

// HTTPCheckTarget defines parameters for an HTTP health check.
// For gateway route validation, set Host to the virtual host (e.g. "billing-api.dev.useclarus.app").
type HTTPCheckTarget struct {
	URL        string
	Host       string // optional; overrides the Host header (for gateway route checks)
	CheckType  string // TypeHTTP or TypeGatewayRoute; defaults to TypeHTTP
	Config     CheckConfig
	DeploymentID string
	Application  string
	ContainerName string
}

// TCPCheckTarget defines parameters for a TCP connectivity check.
type TCPCheckTarget struct {
	Address string // "host:port"
	Config  CheckConfig
}

// ContainerStatus holds the minimal fields from a docker.ContainerInfo that
// the health checker needs. Callers convert docker.ContainerInfo → ContainerStatus
// so that the health package does not depend on the docker package.
type ContainerStatus struct {
	Name     string
	Running  bool
	Status   string // "running", "exited", "dead", etc.
	ExitCode int
}

// Result holds the outcome of a health check.
// The fields match the reporting model from docs/specs/11-health-checks.md.
type Result struct {
	Status       string
	Type         string
	Target       string
	Attempts     int
	Duration     time.Duration
	StatusCode   int    // populated for HTTP checks
	ErrorCode    string
	ErrorMessage string
}

func (r *Result) Healthy() bool { return r.Status == StatusHealthy }
