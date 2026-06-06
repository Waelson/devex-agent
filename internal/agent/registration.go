package agent

import (
	"context"
	"time"

	"devex-agent/internal/platform"
	"devex-agent/internal/state"
)

// ensureRegistered loads the persisted agent identity or registers with the Platform API
// on first boot. On success, a.agentID is set and the platform client is configured.
func (a *RuntimeAgent) ensureRegistered(ctx context.Context) error {
	identity, err := a.deps.State.LoadAgentIdentity()
	if err != nil {
		return err
	}

	if identity != nil && identity.AgentID != "" {
		a.agentID = identity.AgentID
		a.deps.Platform.SetAgentID(a.agentID)
		a.logger.Info("agent identity loaded from disk", "agent_id", a.agentID)
		return nil
	}

	// First boot: register with the Platform API.
	req := platform.RegisterRequest{
		Mode:        a.cfg.Agent.Mode,
		Environment: a.cfg.Agent.Environment,
		Role:        a.cfg.Agent.Role,
		Hostname:    a.hostname,
		InstanceID:  a.instanceID,
		PrivateIP:   a.privateIP,
		Version:     AgentVersion,
		Capabilities: platform.NewRuntimeCapabilities(
			[]string{a.cfg.Agent.Role},
			a.cfg.Runtime.MaxActiveContainers,
			a.cfg.Ports.From,
			a.cfg.Ports.To,
		),
	}

	resp, err := a.deps.Platform.Register(ctx, req)
	if err != nil {
		return err
	}

	a.agentID = resp.AgentID
	a.deps.Platform.SetAgentID(a.agentID)

	now := time.Now().UTC()
	newIdentity := &state.AgentIdentity{
		AgentID:      resp.AgentID,
		Mode:         a.cfg.Agent.Mode,
		Environment:  a.cfg.Agent.Environment,
		Role:         a.cfg.Agent.Role,
		InstanceID:   a.instanceID,
		PrivateIP:    a.privateIP,
		RegisteredAt: now,
		LastSeenAt:   now,
	}

	// Persist identity. Warn on failure but don't abort — the agent will re-register
	// on next restart, which is safe.
	if saveErr := a.deps.State.SaveAgentIdentity(newIdentity); saveErr != nil {
		a.logger.Warn("failed to persist agent identity; will re-register on next restart",
			"error", saveErr)
	}

	a.logger.Info("agent registered with platform",
		"agent_id", a.agentID,
		"status", resp.Status,
	)
	return nil
}
