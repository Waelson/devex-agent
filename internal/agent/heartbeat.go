package agent

import (
	"context"
	"time"

	"devex-agent/internal/platform"
	"devex-agent/internal/state"
)

const heartbeatInterval = 30 * time.Second

// heartbeatLoop sends a heartbeat immediately, then at a fixed interval.
// It logs failures but never stops the agent.
func (a *RuntimeAgent) heartbeatLoop(ctx context.Context) {
	a.sendHeartbeat(ctx)

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.sendHeartbeat(ctx)
		}
	}
}

// sendHeartbeat builds and sends a single heartbeat request to the Platform API.
func (a *RuntimeAgent) sendHeartbeat(ctx context.Context) {
	a.mu.Lock()
	ls := a.localState
	a.mu.Unlock()

	var (
		lastCommandID           string
		lastSuccessfulCommandID string
		activeDeployments       int
	)
	if ls != nil {
		lastCommandID = ls.LastAppliedCommandID
		lastSuccessfulCommandID = ls.LastSuccessfulCommandID
		for _, d := range ls.Deployments {
			if d.Status == state.DeploymentStatusActive {
				activeDeployments++
			}
		}
	}

	allocatedPorts := 0
	if snapshot, err := a.deps.Ports.Snapshot(); err == nil && snapshot != nil {
		allocatedPorts = len(snapshot.Allocations)
	}

	req := platform.HeartbeatRequest{
		Status:                  "online",
		Mode:                    a.cfg.Agent.Mode,
		Environment:             a.cfg.Agent.Environment,
		Role:                    a.cfg.Agent.Role,
		Version:                 AgentVersion,
		PrivateIP:               a.privateIP,
		ActiveDeployments:       activeDeployments,
		AllocatedPorts:          allocatedPorts,
		LastCommandID:           lastCommandID,
		LastSuccessfulCommandID: lastSuccessfulCommandID,
	}

	hbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if _, err := a.deps.Platform.SendHeartbeat(hbCtx, req); err != nil {
		a.logger.Warn("heartbeat failed", "error", err)
	}
}

// drainCleanupLoop periodically removes containers that have been in draining state
// longer than the configured grace period.
func (a *RuntimeAgent) drainCleanupLoop(ctx context.Context) {
	gracePeriod := time.Duration(a.cfg.Runtime.DrainingGracePeriodSecs) * time.Second
	if gracePeriod <= 0 {
		gracePeriod = 5 * time.Minute
	}

	// Check 4 times per grace period so we don't overshoot by too long.
	checkInterval := gracePeriod / 4
	if checkInterval < time.Minute {
		checkInterval = time.Minute
	}

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.cleanupExpiredDraining(ctx, gracePeriod)
		}
	}
}

// cleanupExpiredDraining stops, removes, and de-allocates containers that have been in
// draining state longer than gracePeriod.
func (a *RuntimeAgent) cleanupExpiredDraining(ctx context.Context, gracePeriod time.Duration) {
	a.mu.Lock()
	if a.localState == nil {
		a.mu.Unlock()
		return
	}

	now := time.Now().UTC()
	var expired []state.DeploymentEntry
	for _, d := range a.localState.Deployments {
		if d.Status == state.DeploymentStatusDraining &&
			d.DrainingStartedAt != nil &&
			now.After(d.DrainingStartedAt.Add(gracePeriod)) {
			expired = append(expired, d)
		}
	}
	a.mu.Unlock()

	for _, entry := range expired {
		a.logger.Info("removing expired draining container",
			"container_name", entry.ContainerName,
			"deployment_id", entry.DeploymentID,
		)

		stopCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
		if err := a.deps.Docker.StopContainer(stopCtx, entry.ContainerName, 30*time.Second); err != nil {
			a.logger.Warn("stop failed during drain cleanup; continuing",
				"container_name", entry.ContainerName, "error", err)
		}
		cancel()

		rmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := a.deps.Docker.RemoveContainer(rmCtx, entry.ContainerName); err != nil {
			a.logger.Warn("remove failed during drain cleanup; continuing",
				"container_name", entry.ContainerName, "error", err)
		}
		cancel()

		if entry.HostPort > 0 {
			if err := a.deps.Ports.Release(entry.HostPort); err != nil {
				a.logger.Warn("port release failed during drain cleanup",
					"port", entry.HostPort, "error", err)
			}
		}

		if err := a.modifyState(func(ls *state.LocalState) {
			removeDeploymentByID(ls, entry.DeploymentID)
		}); err != nil {
			a.logger.Warn("state update failed after drain cleanup",
				"deployment_id", entry.DeploymentID, "error", err)
		}

		a.logger.Info("expired draining container removed",
			"container_name", entry.ContainerName,
			"deployment_id", entry.DeploymentID,
		)
	}
}
