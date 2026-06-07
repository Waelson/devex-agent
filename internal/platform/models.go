package platform

import (
	"encoding/json"
	"time"
)

// Command type constants.
const (
	CommandTypeDeployApplication  = "DEPLOY_APPLICATION"
	CommandTypeStopApplication    = "STOP_APPLICATION"
	CommandTypeRemoveDeployment   = "REMOVE_DEPLOYMENT"
	CommandTypeCleanupDraining    = "CLEANUP_DRAINING"
	CommandTypeMarkDraining       = "MARK_DRAINING"
	CommandTypeApplyGatewayRoutes = "APPLY_GATEWAY_ROUTES"
	CommandTypeReconcile          = "RECONCILE"
)

// RegisterRequest is sent to POST /api/agents/register.
// Capabilities is mode-specific; use NewRuntimeCapabilities or NewGatewayCapabilities.
type RegisterRequest struct {
	Mode         string         `json:"mode"`
	Environment  string         `json:"environment"`
	Role         string         `json:"role"`
	Hostname     string         `json:"hostname"`
	InstanceID   string         `json:"instance_id"`
	PrivateIP    string         `json:"private_ip"`
	PublicIP     *string        `json:"public_ip"`
	Version      string         `json:"version"`
	Capabilities map[string]any `json:"capabilities"`
}

// NewRuntimeCapabilities builds the capabilities map for a Runtime Agent register request.
func NewRuntimeCapabilities(workloadTypes []string, maxContainers, portFrom, portTo int) map[string]any {
	return map[string]any{
		"workload_types":        workloadTypes,
		"max_active_containers": maxContainers,
		"port_range": map[string]any{
			"from": portFrom,
			"to":   portTo,
		},
	}
}

// NewGatewayCapabilities builds the capabilities map for a Gateway Agent register request.
func NewGatewayCapabilities(caddyAdminURL string) map[string]any {
	return map[string]any{
		"gateway":          true,
		"caddy_admin_url":  caddyAdminURL,
	}
}

// RegisterResponse is returned by POST /api/agents/register.
type RegisterResponse struct {
	AgentID string `json:"agent_id"`
	Status  string `json:"status"`
}

// HeartbeatRequest is sent to POST /api/agents/{agent_id}/heartbeat.
// Fields are populated depending on agent mode; zero values are omitted.
type HeartbeatRequest struct {
	Status      string `json:"status"`
	Mode        string `json:"mode"`
	Environment string `json:"environment"`
	Role        string `json:"role"`
	Version     string `json:"version"`
	PrivateIP   string `json:"private_ip,omitempty"`

	// Runtime Agent fields.
	RunningContainers       int    `json:"running_containers,omitempty"`
	ActiveDeployments       int    `json:"active_deployments,omitempty"`
	AllocatedPorts          int    `json:"allocated_ports,omitempty"`
	LastCommandID           string `json:"last_command_id,omitempty"`
	LastSuccessfulCommandID string `json:"last_successful_command_id,omitempty"`

	// Gateway Agent fields.
	CaddyStatus                       string `json:"caddy_status,omitempty"`
	RoutesTotal                       int    `json:"routes_total,omitempty"`
	LastAppliedDesiredStateVersion    int    `json:"last_applied_desired_state_version,omitempty"`
	LastSuccessfulDesiredStateVersion int    `json:"last_successful_desired_state_version,omitempty"`
}

// HeartbeatResponse is returned by POST /api/agents/{agent_id}/heartbeat.
type HeartbeatResponse struct {
	Status     string    `json:"status"`
	ServerTime time.Time `json:"server_time"`
}

// PendingCommand is an element returned by GET /api/agents/{agent_id}/commands/pending.
// Payload is kept as raw JSON; callers decode it with json.Unmarshal into the
// appropriate payload struct for the command Type.
type PendingCommand struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	DeploymentID  string          `json:"deployment_id"`
	TargetAgentID string          `json:"target_agent_id"`
	Status        string          `json:"status"`
	TimeoutSecs   int             `json:"timeout_seconds"`
	CreatedAt     time.Time       `json:"created_at"`
	Payload       json.RawMessage `json:"payload"`
}

// ClaimResponse is returned by POST /api/agents/{agent_id}/commands/{id}/claim.
type ClaimResponse struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	ClaimedBy string    `json:"claimed_by"`
	ClaimedAt time.Time `json:"claimed_at"`
}

// StartResponse is returned by POST /api/agents/{agent_id}/commands/{id}/start.
type StartResponse struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
}

// CommandResult holds deploy-success details for a succeeded command report.
type CommandResult struct {
	Application           string `json:"application,omitempty"`
	Environment           string `json:"environment,omitempty"`
	ContainerName         string `json:"container_name,omitempty"`
	Image                 string `json:"image,omitempty"`
	RuntimePrivateIP      string `json:"runtime_private_ip,omitempty"`
	HostPort              int    `json:"host_port,omitempty"`
	ContainerInternalPort int    `json:"container_internal_port,omitempty"`
	Health                string `json:"health,omitempty"`
	RequiresRoute         bool   `json:"requires_route,omitempty"`
}

// CommandErrorPayload is the structured error sent in a failed command report.
type CommandErrorPayload struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Operation string `json:"operation,omitempty"`
	Retryable bool   `json:"retryable"`
}

// CommandReportRequest is sent to POST /api/agents/{agent_id}/commands/{id}/report.
// Set Status to "succeeded" with a Result, or "failed" with an Error.
type CommandReportRequest struct {
	Status       string               `json:"status"`
	DeploymentID string               `json:"deployment_id,omitempty"`
	Result       *CommandResult       `json:"result,omitempty"`
	Error        *CommandErrorPayload `json:"error,omitempty"`
}

// DesiredDeployment is one entry in a runtime desired state response.
type DesiredDeployment struct {
	DeploymentID          string `json:"deployment_id"`
	Application           string `json:"application"`
	ContainerName         string `json:"container_name"`
	Image                 string `json:"image"`
	HostPort              int    `json:"host_port"`
	ContainerInternalPort int    `json:"container_internal_port"`
	Status                string `json:"status"`
}

// DesiredRoute is one route entry in a gateway desired state response.
type DesiredRoute struct {
	ID              string `json:"id"`
	Host            string `json:"host"`
	Path            string `json:"path"`
	Upstream        string `json:"upstream"`
	DeploymentID    string `json:"deployment_id"`
	HealthCheckPath string `json:"health_check_path"`
}

// DesiredState is returned by GET /api/agents/{agent_id}/desired-state.
// Type is "runtime_deployments" or "gateway_routes".
type DesiredState struct {
	Version     int                 `json:"version"`
	Type        string              `json:"type"`
	Environment string              `json:"environment"`
	Deployments []DesiredDeployment `json:"deployments,omitempty"`
	Routes      []DesiredRoute      `json:"routes,omitempty"`
}

// DesiredStateReportRequest is sent to POST /api/agents/{agent_id}/desired-state/report.
type DesiredStateReportRequest struct {
	Status              string               `json:"status"`
	DesiredStateVersion int                  `json:"desired_state_version"`
	Type                string               `json:"type"`
	Environment         string               `json:"environment"`
	RoutesTotal         int                  `json:"routes_total,omitempty"`
	ValidatedRoutes     int                  `json:"validated_routes,omitempty"`
	FailedRoutes        int                  `json:"failed_routes,omitempty"`
	AppliedAt           *time.Time           `json:"applied_at,omitempty"`
	Error               *CommandErrorPayload `json:"error,omitempty"`
}

// apiErrorEnvelope is used internally to decode Platform API error responses.
type apiErrorEnvelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}
