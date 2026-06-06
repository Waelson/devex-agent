package state

import "time"

// DeploymentStatus represents the local lifecycle status of a deployment.
type DeploymentStatus string

const (
	DeploymentStatusReserved       DeploymentStatus = "reserved"
	DeploymentStatusStarting       DeploymentStatus = "starting"
	DeploymentStatusCheckingHealth DeploymentStatus = "checking_health"
	DeploymentStatusActive         DeploymentStatus = "active"
	DeploymentStatusDraining       DeploymentStatus = "draining"
	DeploymentStatusFailed         DeploymentStatus = "failed"
	DeploymentStatusRemoved        DeploymentStatus = "removed"
	DeploymentStatusOrphaned       DeploymentStatus = "orphaned"
	DeploymentStatusInconsistent   DeploymentStatus = "inconsistent"
)

// AgentIdentity is persisted to agent.json and survives agent restarts.
type AgentIdentity struct {
	SchemaVersion int       `json:"schema_version"`
	AgentID       string    `json:"agent_id"`
	Mode          string    `json:"mode"`
	Environment   string    `json:"environment"`
	Role          string    `json:"role"`
	InstanceID    string    `json:"instance_id"`
	PrivateIP     string    `json:"private_ip"`
	RegisteredAt  time.Time `json:"registered_at"`
	LastSeenAt    time.Time `json:"last_seen_at"`
}

// DeploymentEntry tracks a single deployment managed by the Runtime Agent.
type DeploymentEntry struct {
	DeploymentID          string           `json:"deployment_id"`
	Application           string           `json:"application"`
	Environment           string           `json:"environment"`
	Image                 string           `json:"image"`
	ContainerName         string           `json:"container_name"`
	HostPort              int              `json:"host_port"`
	ContainerInternalPort int              `json:"container_internal_port"`
	Status                DeploymentStatus `json:"status"`
	RequiresRoute         bool             `json:"requires_route"`
	CreatedAt             time.Time        `json:"created_at"`
	UpdatedAt             time.Time        `json:"updated_at"`
	DrainingStartedAt     *time.Time       `json:"draining_started_at,omitempty"`
}

// LocalState is the operational state of the Runtime Agent, persisted to state.json.
type LocalState struct {
	SchemaVersion           int               `json:"schema_version"`
	AgentID                 string            `json:"agent_id"`
	Mode                    string            `json:"mode"`
	Environment             string            `json:"environment"`
	Role                    string            `json:"role"`
	LastAppliedCommandID    string            `json:"last_applied_command_id"`
	LastSuccessfulCommandID string            `json:"last_successful_command_id"`
	Deployments             []DeploymentEntry `json:"deployments"`
}
