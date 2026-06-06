package ports

import (
	"context"
	"strconv"
	"sync"
	"time"

	aerrors "devex-agent/internal/errors"
)

// Manager allocates and tracks host ports for Docker containers.
// All mutations are serialised by an in-memory mutex (sufficient for the MVP
// where the agent processes one mutating command at a time).
type Manager struct {
	from      int
	to        int
	maxActive int
	path      string
	mu        sync.Mutex
}

// NewManager creates a Manager for the given port range.
// path is the absolute path to ports.json.
func NewManager(from, to, maxActive int, path string) (*Manager, error) {
	if from > to {
		return nil, aerrors.Newf(aerrors.CodePortAllocationFailed,
			"invalid port range: from %d > to %d", from, to)
	}
	if maxActive <= 0 {
		return nil, aerrors.Newf(aerrors.CodePortAllocationFailed,
			"max_active_containers must be > 0, got %d", maxActive)
	}
	return &Manager{from: from, to: to, maxActive: maxActive, path: path}, nil
}

// Allocate reserves an available host port for the given deployment.
//
// If spec.DeploymentID already holds a reserved or active port, the existing
// port is returned without consuming a new one (idempotency).
//
// Returns CodePortAllocationFailed when the active-container limit is reached,
// or CodePortRangeExhausted when the entire range is occupied.
func (m *Manager) Allocate(_ context.Context, spec AllocateSpec) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, err := loadPortState(m.path)
	if err != nil {
		return 0, err
	}

	// Idempotency: re-use port already held by this deployment.
	if spec.DeploymentID != "" {
		for key, alloc := range state.Allocations {
			if alloc.DeploymentID == spec.DeploymentID &&
				(alloc.Status == PortStatusReserved || alloc.Status == PortStatusActive) {
				port, _ := strconv.Atoi(key)
				return port, nil
			}
		}
	}

	// Find first available port (absent from or explicitly available in the map).
	port := 0
	for p := m.from; p <= m.to; p++ {
		alloc, exists := state.Allocations[portKey(p)]
		if !exists || alloc.Status == PortStatusAvailable {
			port = p
			break
		}
	}
	if port == 0 {
		return 0, aerrors.Newf(aerrors.CodePortRangeExhausted,
			"no available ports in range %d-%d", m.from, m.to)
	}

	// Mark as reserved.
	state.Allocations[portKey(port)] = &PortAllocation{
		Status:                PortStatusReserved,
		DeploymentID:          spec.DeploymentID,
		Application:           spec.Application,
		ContainerName:         spec.ContainerName,
		ContainerInternalPort: spec.ContainerInternalPort,
		AllocatedAt:           time.Now().UTC(),
	}

	if err := savePortState(m.path, state); err != nil {
		return 0, err
	}
	return port, nil
}

// MarkActive transitions a port from reserved → active.
// Called after the container has passed its health check.
// Returns CodePortAllocationFailed when the active-container limit is already reached.
func (m *Manager) MarkActive(port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, err := loadPortState(m.path)
	if err != nil {
		return err
	}

	alloc, ok := state.Allocations[portKey(port)]
	if !ok {
		return aerrors.Newf(aerrors.CodePortStateInconsistent,
			"port %d is not tracked; cannot mark active", port)
	}
	if alloc.Status != PortStatusReserved && alloc.Status != PortStatusActive {
		return aerrors.Newf(aerrors.CodePortStateInconsistent,
			"cannot mark port %d active: current status is %q", port, alloc.Status)
	}

	// Enforce the active-container limit here, not at Allocate, so that
	// blue/green deploys can start a reserved container while the old one
	// is still active (temporary states are allowed above the limit).
	if alloc.Status == PortStatusReserved {
		active := countByStatus(state, PortStatusActive)
		if active >= m.maxActive {
			return aerrors.Newf(aerrors.CodePortAllocationFailed,
				"active container limit reached (%d/%d); cannot mark port %d active",
				active, m.maxActive, port)
		}
	}

	alloc.Status = PortStatusActive
	return savePortState(m.path, state)
}

// MarkDraining transitions a port from active → draining.
// Called when a new version has been validated and is taking over the route.
func (m *Manager) MarkDraining(port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, err := loadPortState(m.path)
	if err != nil {
		return err
	}

	alloc, ok := state.Allocations[portKey(port)]
	if !ok {
		return aerrors.Newf(aerrors.CodePortStateInconsistent,
			"port %d is not tracked; cannot mark draining", port)
	}
	if alloc.Status != PortStatusActive && alloc.Status != PortStatusDraining {
		return aerrors.Newf(aerrors.CodePortStateInconsistent,
			"cannot mark port %d draining: current status is %q", port, alloc.Status)
	}

	now := time.Now().UTC()
	alloc.Status = PortStatusDraining
	if alloc.DrainingStartedAt == nil {
		alloc.DrainingStartedAt = &now
	}
	return savePortState(m.path, state)
}

// Release makes a port available again by removing it from the allocations map.
// Called after the container has been stopped and removed.
func (m *Manager) Release(port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, err := loadPortState(m.path)
	if err != nil {
		return err
	}

	if _, ok := state.Allocations[portKey(port)]; !ok {
		return aerrors.Newf(aerrors.CodePortStateInconsistent,
			"port %d is not tracked; cannot release", port)
	}

	delete(state.Allocations, portKey(port))
	return savePortState(m.path, state)
}

// CountActive returns the number of ports currently in the active state.
// Used by the heartbeat to report running container count.
func (m *Manager) CountActive() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, err := loadPortState(m.path)
	if err != nil {
		return 0, err
	}
	return countByStatus(state, PortStatusActive), nil
}

// Snapshot returns a read-only copy of the current PortState.
// Useful for diagnostics, heartbeat, and testing.
func (m *Manager) Snapshot() (*PortState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return loadPortState(m.path)
}

// Reconcile audits the tracked allocations against the set of containers
// currently reported as live by the Docker runtime.
//
// liveContainerNames is a set of container names that exist in Docker
// (regardless of running state). Ports referencing a container not in this set
// are released. Ports stuck in the failed or released state are also cleaned up.
//
// Returns the list of changes made.
func (m *Manager) Reconcile(_ context.Context, liveContainerNames map[string]bool) ([]ReconcileEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, err := loadPortState(m.path)
	if err != nil {
		return nil, err
	}

	var events []ReconcileEvent

	for key, alloc := range state.Allocations {
		port, _ := strconv.Atoi(key)

		switch alloc.Status {
		case PortStatusReserved, PortStatusActive, PortStatusDraining:
			// Container must exist in Docker; otherwise the port is stale.
			if alloc.ContainerName != "" && !liveContainerNames[alloc.ContainerName] {
				events = append(events, ReconcileEvent{
					Port:      port,
					OldStatus: alloc.Status,
					Reason:    "container not found in Docker: " + alloc.ContainerName,
				})
				delete(state.Allocations, key)
			}

		case PortStatusFailed, PortStatusReleased:
			// Stale terminal states — free the port.
			events = append(events, ReconcileEvent{
				Port:      port,
				OldStatus: alloc.Status,
				Reason:    "cleanup of terminal state " + string(alloc.Status),
			})
			delete(state.Allocations, key)
		}
	}

	if len(events) == 0 {
		return events, nil
	}
	if err := savePortState(m.path, state); err != nil {
		return nil, err
	}
	return events, nil
}

// --- internal helpers ---

func countByStatus(state *PortState, status PortStatus) int {
	n := 0
	for _, alloc := range state.Allocations {
		if alloc.Status == status {
			n++
		}
	}
	return n
}
