package ports

import "time"

// PortStatus is the lifecycle state of a managed host port.
type PortStatus string

const (
	PortStatusAvailable PortStatus = "available"
	PortStatusReserved  PortStatus = "reserved"
	PortStatusActive    PortStatus = "active"
	PortStatusDraining  PortStatus = "draining"
	PortStatusFailed    PortStatus = "failed"
	PortStatusReleased  PortStatus = "released"
	PortStatusUnmanaged PortStatus = "unmanaged"
)

// PortAllocation records the runtime occupant of a single host port.
// Only non-available ports appear in the PortState allocations map.
type PortAllocation struct {
	Status                PortStatus `json:"status"`
	DeploymentID          string     `json:"deployment_id,omitempty"`
	Application           string     `json:"application,omitempty"`
	ContainerName         string     `json:"container_name,omitempty"`
	ContainerInternalPort int        `json:"container_internal_port,omitempty"`
	AllocatedAt           time.Time  `json:"allocated_at"`
	DrainingStartedAt     *time.Time `json:"draining_started_at,omitempty"`
}

// PortRange is the inclusive host-port range managed by the Runtime Agent.
type PortRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// PortState is the root structure persisted to ports.json.
// Ports absent from Allocations are implicitly available.
type PortState struct {
	SchemaVersion int                        `json:"schema_version"`
	Range         PortRange                  `json:"range"`
	Allocations   map[string]*PortAllocation `json:"allocations"`
}

// AllocateSpec describes the deployment that needs a host port.
type AllocateSpec struct {
	DeploymentID          string
	Application           string
	ContainerName         string
	ContainerInternalPort int
}

// ReconcileEvent records a port state change made during reconciliation.
type ReconcileEvent struct {
	Port      int
	OldStatus PortStatus
	Reason    string
}
