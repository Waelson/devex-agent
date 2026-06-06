package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	aerrors "devex-agent/internal/errors"
	"devex-agent/internal/docker"
	"devex-agent/internal/health"
	"devex-agent/internal/platform"
	"devex-agent/internal/ports"
	"devex-agent/internal/state"
)

// --- command payload types ---

// DeployApplicationPayload is the payload for DEPLOY_APPLICATION commands.
type DeployApplicationPayload struct {
	Application           string            `json:"application"`
	Environment           string            `json:"environment"`
	Image                 string            `json:"image"`
	ContainerName         string            `json:"container_name"`
	ContainerInternalPort int               `json:"container_internal_port"`
	HealthCheckPath       string            `json:"health_check_path"`
	RequiresRoute         bool              `json:"requires_route"`
	EnvironmentVariables  map[string]string `json:"environment_variables"`
	Labels                map[string]string `json:"labels"`
}

// StopApplicationPayload is the payload for STOP_APPLICATION commands.
type StopApplicationPayload struct {
	ContainerName      string `json:"container_name"`
	StopTimeoutSeconds int    `json:"stop_timeout_seconds"`
}

// RemoveDeploymentPayload is the payload for REMOVE_DEPLOYMENT commands.
type RemoveDeploymentPayload struct {
	DeploymentID  string `json:"deployment_id"`
	ContainerName string `json:"container_name"`
	ReleasePort   bool   `json:"release_port"`
}

// CleanupDrainingPayload is the payload for CLEANUP_DRAINING commands.
type CleanupDrainingPayload struct {
	OlderThanSeconds int `json:"older_than_seconds"`
}

// MarkDrainingPayload is the payload for MARK_DRAINING commands.
// The Platform sends this after Caddy traffic has been switched to a new
// deployment, signalling that the old deployment should enter the draining window.
type MarkDrainingPayload struct {
	DeploymentID  string `json:"deployment_id"`
	ContainerName string `json:"container_name"`
}

// ReconcilePayload is the payload for RECONCILE commands.
// Scope controls what is reconciled: "ports", "deployments", or "all" (default).
type ReconcilePayload struct {
	Scope string `json:"scope"`
}

// --- poll loop ---

// pollAndProcessCommands fetches pending commands and processes the first one.
// Commands are processed one at a time (serialized by the poll loop).
func (a *RuntimeAgent) pollAndProcessCommands(ctx context.Context) {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	commands, err := a.deps.Platform.FetchPendingCommands(fetchCtx)
	cancel()
	if err != nil {
		a.logger.Warn("failed to fetch pending commands", "error", err)
		return
	}
	if len(commands) == 0 {
		return
	}

	cmd := commands[0]
	log := a.logger.With(
		"command_id", cmd.ID,
		"type", cmd.Type,
		"deployment_id", cmd.DeploymentID,
	)
	log.Info("claiming command")

	claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, claimErr := a.deps.Platform.ClaimCommand(claimCtx, cmd.ID)
	cancel()
	if claimErr != nil {
		log.Warn("failed to claim command; skipping", "error", claimErr)
		return
	}
	log.Info("command claimed")

	// Transition to running state.
	startCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, startErr := a.deps.Platform.StartCommand(startCtx, cmd.ID)
	cancel()
	if startErr != nil {
		// Non-fatal: we still own the command and must report its outcome.
		log.Warn("failed to mark command as running; will continue", "error", startErr)
	}

	// Record that we attempted this command.
	_ = a.modifyState(func(ls *state.LocalState) {
		ls.LastAppliedCommandID = cmd.ID
	})

	if err := a.executeCommand(ctx, cmd); err != nil {
		log.Error("command finished with unreported error", "error", err)
	}
}

// executeCommand routes the command to the appropriate handler.
func (a *RuntimeAgent) executeCommand(ctx context.Context, cmd platform.PendingCommand) error {
	execCtx := ctx
	var cancel context.CancelFunc
	if cmd.TimeoutSecs > 0 {
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(cmd.TimeoutSecs)*time.Second)
		defer cancel()
	}

	switch cmd.Type {
	case platform.CommandTypeDeployApplication:
		return a.executeDeployApplication(execCtx, cmd)
	case platform.CommandTypeStopApplication:
		return a.executeStopApplication(execCtx, cmd)
	case platform.CommandTypeRemoveDeployment:
		return a.executeRemoveDeployment(execCtx, cmd)
	case platform.CommandTypeCleanupDraining:
		return a.executeCleanupDraining(execCtx, cmd)
	case platform.CommandTypeMarkDraining:
		return a.executeMarkDraining(execCtx, cmd)
	case platform.CommandTypeReconcile:
		return a.executeReconcile(execCtx, cmd)
	default:
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID,
			aerrors.Newf(aerrors.CodeCommandInvalid, "unknown command type %q", cmd.Type))
	}
}

// --- DEPLOY_APPLICATION ---

func (a *RuntimeAgent) executeDeployApplication(ctx context.Context, cmd platform.PendingCommand) error {
	var p DeployApplicationPayload
	if err := json.Unmarshal(cmd.Payload, &p); err != nil {
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID,
			aerrors.Newf(aerrors.CodeCommandInvalid, "parse DEPLOY_APPLICATION payload: %s", err))
	}
	if p.Application == "" || p.Image == "" || p.ContainerName == "" {
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID,
			aerrors.New(aerrors.CodeCommandInvalid, "payload missing required fields: application, image, container_name"))
	}

	log := a.logger.With(
		"command_id", cmd.ID,
		"deployment_id", cmd.DeploymentID,
		"application", p.Application,
		"container_name", p.ContainerName,
	)

	// Idempotency: if deployment is already active, report success without re-deploying.
	if existing := a.findDeployment(cmd.DeploymentID); existing != nil &&
		existing.Status == state.DeploymentStatusActive {
		log.Info("deployment already active; reporting success (idempotent)")
		return a.reportSuccess(ctx, cmd.ID, cmd.DeploymentID, &platform.CommandResult{
			Application:           p.Application,
			Environment:           p.Environment,
			ContainerName:         existing.ContainerName,
			Image:                 existing.Image,
			RuntimePrivateIP:      a.privateIP,
			HostPort:              existing.HostPort,
			ContainerInternalPort: existing.ContainerInternalPort,
			Health:                "healthy",
			RequiresRoute:         p.RequiresRoute,
		})
	}

	// Register starting state.
	now := time.Now().UTC()
	_ = a.modifyState(func(ls *state.LocalState) {
		upsertDeployment(ls, state.DeploymentEntry{
			DeploymentID:          cmd.DeploymentID,
			Application:           p.Application,
			Environment:           p.Environment,
			Image:                 p.Image,
			ContainerName:         p.ContainerName,
			ContainerInternalPort: p.ContainerInternalPort,
			Status:                state.DeploymentStatusStarting,
			RequiresRoute:         p.RequiresRoute,
			CreatedAt:             now,
			UpdatedAt:             now,
		})
	})

	// 1. Pull image.
	log.Info("pulling image", "image", p.Image)
	pullTimeout := 5 * time.Minute
	if a.cfg.Docker.PullTimeoutSecs > 0 {
		pullTimeout = time.Duration(a.cfg.Docker.PullTimeoutSecs) * time.Second
	}
	pullCtx, cancel := context.WithTimeout(ctx, pullTimeout)
	pullErr := a.deps.Docker.PullImage(pullCtx, p.Image)
	cancel()
	if pullErr != nil {
		log.Error("image pull failed", "image", p.Image, "error", pullErr)
		_ = a.modifyState(func(ls *state.LocalState) { removeDeploymentByID(ls, cmd.DeploymentID) })
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID, pullErr)
	}
	log.Info("image pulled", "image", p.Image)

	// 2. Allocate host port (skip for workers without a port).
	var hostPort int
	if p.ContainerInternalPort > 0 {
		allocCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var allocErr error
		hostPort, allocErr = a.deps.Ports.Allocate(allocCtx, ports.AllocateSpec{
			DeploymentID:          cmd.DeploymentID,
			Application:           p.Application,
			ContainerName:         p.ContainerName,
			ContainerInternalPort: p.ContainerInternalPort,
		})
		cancel()
		if allocErr != nil {
			log.Error("port allocation failed", "error", allocErr)
			_ = a.modifyState(func(ls *state.LocalState) { removeDeploymentByID(ls, cmd.DeploymentID) })
			return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID, allocErr)
		}
		log.Info("port allocated", "host_port", hostPort)
	}

	// 3. Merge managed labels with any labels from the Platform.
	labels := buildManagedLabels(cmd.ID, cmd.DeploymentID, p.Application, p.Environment, a.agentID)
	for k, v := range p.Labels {
		labels[k] = v
	}

	// 4. Start container.
	startTimeout := 30 * time.Second
	if a.cfg.Docker.StartTimeoutSecs > 0 {
		startTimeout = time.Duration(a.cfg.Docker.StartTimeoutSecs) * time.Second
	}
	startCtx, cancel := context.WithTimeout(ctx, startTimeout)
	containerInfo, startErr := a.deps.Docker.StartContainer(startCtx, docker.ContainerSpec{
		Name:          p.ContainerName,
		Image:         p.Image,
		HostPort:      hostPort,
		ContainerPort: p.ContainerInternalPort,
		Env:           p.EnvironmentVariables,
		Labels:        labels,
		RestartPolicy: "unless-stopped",
	})
	cancel()
	if startErr != nil {
		log.Error("container start failed", "error", startErr)
		if hostPort > 0 {
			_ = a.deps.Ports.Release(hostPort)
		}
		_ = a.modifyState(func(ls *state.LocalState) { removeDeploymentByID(ls, cmd.DeploymentID) })
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID, startErr)
	}
	log.Info("container started",
		"container_id", truncateID(containerInfo.ID),
		"host_port", hostPort,
	)

	// Update state with port and checking-health status.
	_ = a.modifyState(func(ls *state.LocalState) {
		if d := findDeploymentByID(ls, cmd.DeploymentID); d != nil {
			d.HostPort = hostPort
			d.Status = state.DeploymentStatusCheckingHealth
			d.UpdatedAt = time.Now().UTC()
		}
	})

	// 5. Health check.
	hcCfg := health.CheckConfig{
		TimeoutSeconds:  a.cfg.HealthCheck.TimeoutSecs,
		IntervalSeconds: a.cfg.HealthCheck.IntervalSecs,
		Retries:         a.cfg.HealthCheck.Retries,
	}
	if hcCfg.TimeoutSeconds <= 0 {
		hcCfg.TimeoutSeconds = 2
	}
	if hcCfg.Retries <= 0 {
		hcCfg.Retries = 6
	}

	var healthErr error
	if hostPort > 0 && p.HealthCheckPath != "" {
		target := fmt.Sprintf("http://127.0.0.1:%d%s", hostPort, p.HealthCheckPath)
		log.Info("running HTTP health check", "target", target)
		hcTimeout := healthCheckTotalTimeout(hcCfg)
		hcCtx, cancel := context.WithTimeout(ctx, hcTimeout)
		_, healthErr = a.deps.Health.CheckHTTP(hcCtx, health.HTTPCheckTarget{
			URL:    target,
			Config: hcCfg,
		})
		cancel()
	} else {
		// Worker or container-only check.
		inspectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		info, inspectErr := a.deps.Docker.InspectContainer(inspectCtx, p.ContainerName)
		cancel()
		if inspectErr != nil {
			healthErr = inspectErr
		} else {
			_, healthErr = a.deps.Health.CheckContainer(ctx, health.ContainerStatus{
				Name:     info.Name,
				Running:  info.Running,
				Status:   info.Status,
				ExitCode: info.ExitCode,
			})
		}
	}

	if healthErr != nil {
		log.Error("health check failed", "error", healthErr)
		stopCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
		if err := a.deps.Docker.StopContainer(stopCtx, p.ContainerName, 30*time.Second); err != nil {
			log.Warn("stop failed during health check cleanup", "error", err)
		}
		cancel()
		rmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := a.deps.Docker.RemoveContainer(rmCtx, p.ContainerName); err != nil {
			log.Warn("remove failed during health check cleanup", "error", err)
		}
		cancel()
		if hostPort > 0 {
			if err := a.deps.Ports.Release(hostPort); err != nil {
				log.Warn("port release failed during health check cleanup", "port", hostPort, "error", err)
			}
		}
		_ = a.modifyState(func(ls *state.LocalState) { removeDeploymentByID(ls, cmd.DeploymentID) })
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID, healthErr)
	}
	log.Info("health check passed")

	// 6. Mark port active.
	if hostPort > 0 {
		if err := a.deps.Ports.MarkActive(hostPort); err != nil {
			log.Warn("failed to mark port active; continuing", "port", hostPort, "error", err)
		}
	}

	// 7. Update state to active.
	_ = a.modifyState(func(ls *state.LocalState) {
		if d := findDeploymentByID(ls, cmd.DeploymentID); d != nil {
			d.Status = state.DeploymentStatusActive
			d.UpdatedAt = time.Now().UTC()
		}
	})

	log.Info("deploy succeeded", "host_port", hostPort)
	return a.reportSuccess(ctx, cmd.ID, cmd.DeploymentID, &platform.CommandResult{
		Application:           p.Application,
		Environment:           p.Environment,
		ContainerName:         p.ContainerName,
		Image:                 p.Image,
		RuntimePrivateIP:      a.privateIP,
		HostPort:              hostPort,
		ContainerInternalPort: p.ContainerInternalPort,
		Health:                "healthy",
		RequiresRoute:         p.RequiresRoute,
	})
}

// --- STOP_APPLICATION ---

func (a *RuntimeAgent) executeStopApplication(ctx context.Context, cmd platform.PendingCommand) error {
	var p StopApplicationPayload
	if err := json.Unmarshal(cmd.Payload, &p); err != nil {
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID,
			aerrors.Newf(aerrors.CodeCommandInvalid, "parse STOP_APPLICATION payload: %s", err))
	}
	if p.ContainerName == "" {
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID,
			aerrors.New(aerrors.CodeCommandInvalid, "container_name is required"))
	}

	stopTimeout := time.Duration(p.StopTimeoutSeconds) * time.Second
	if stopTimeout <= 0 {
		stopTimeout = time.Duration(a.cfg.Docker.StopTimeoutSecs) * time.Second
	}
	if stopTimeout <= 0 {
		stopTimeout = 30 * time.Second
	}

	stopCtx, cancel := context.WithTimeout(ctx, stopTimeout+5*time.Second)
	err := a.deps.Docker.StopContainer(stopCtx, p.ContainerName, stopTimeout)
	cancel()
	if err != nil && aerrors.CodeOf(err) != aerrors.CodeContainerNotFound {
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID, err)
	}

	a.logger.Info("container stopped", "container_name", p.ContainerName)
	return a.reportSuccess(ctx, cmd.ID, cmd.DeploymentID, nil)
}

// --- REMOVE_DEPLOYMENT ---

func (a *RuntimeAgent) executeRemoveDeployment(ctx context.Context, cmd platform.PendingCommand) error {
	var p RemoveDeploymentPayload
	if err := json.Unmarshal(cmd.Payload, &p); err != nil {
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID,
			aerrors.Newf(aerrors.CodeCommandInvalid, "parse REMOVE_DEPLOYMENT payload: %s", err))
	}
	if p.ContainerName == "" {
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID,
			aerrors.New(aerrors.CodeCommandInvalid, "container_name is required"))
	}

	// Read host port from state before removing the entry.
	var hostPort int
	if existing := a.findDeployment(p.DeploymentID); existing != nil {
		hostPort = existing.HostPort
	}

	stopCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	if err := a.deps.Docker.StopContainer(stopCtx, p.ContainerName, 30*time.Second); err != nil &&
		aerrors.CodeOf(err) != aerrors.CodeContainerNotFound {
		a.logger.Warn("stop failed during remove; continuing", "container_name", p.ContainerName, "error", err)
	}
	cancel()

	rmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	rmErr := a.deps.Docker.RemoveContainer(rmCtx, p.ContainerName)
	cancel()
	if rmErr != nil && aerrors.CodeOf(rmErr) != aerrors.CodeContainerNotFound {
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID, rmErr)
	}

	if p.ReleasePort && hostPort > 0 {
		if err := a.deps.Ports.Release(hostPort); err != nil {
			a.logger.Warn("port release failed during remove", "port", hostPort, "error", err)
		}
	}

	_ = a.modifyState(func(ls *state.LocalState) {
		removeDeploymentByID(ls, p.DeploymentID)
	})

	a.logger.Info("deployment removed", "container_name", p.ContainerName, "deployment_id", p.DeploymentID)
	return a.reportSuccess(ctx, cmd.ID, cmd.DeploymentID, nil)
}

// --- CLEANUP_DRAINING ---

func (a *RuntimeAgent) executeCleanupDraining(ctx context.Context, cmd platform.PendingCommand) error {
	var p CleanupDrainingPayload
	if err := json.Unmarshal(cmd.Payload, &p); err != nil {
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID,
			aerrors.Newf(aerrors.CodeCommandInvalid, "parse CLEANUP_DRAINING payload: %s", err))
	}

	gracePeriod := time.Duration(p.OlderThanSeconds) * time.Second
	if gracePeriod <= 0 {
		gracePeriod = time.Duration(a.cfg.Runtime.DrainingGracePeriodSecs) * time.Second
	}
	if gracePeriod <= 0 {
		gracePeriod = 5 * time.Minute
	}

	a.cleanupExpiredDraining(ctx, gracePeriod)
	return a.reportSuccess(ctx, cmd.ID, cmd.DeploymentID, nil)
}

// --- MARK_DRAINING ---

// executeMarkDraining transitions a deployment from active to draining.
// The Platform sends this command after Caddy traffic has been switched to a newer
// deployment version so that the old version can be cleaned up after the grace period.
// The operation is idempotent: if the deployment is already draining or not found,
// it reports success.
func (a *RuntimeAgent) executeMarkDraining(ctx context.Context, cmd platform.PendingCommand) error {
	var p MarkDrainingPayload
	if err := json.Unmarshal(cmd.Payload, &p); err != nil {
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID,
			aerrors.Newf(aerrors.CodeCommandInvalid, "parse MARK_DRAINING payload: %s", err))
	}
	if p.DeploymentID == "" {
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID,
			aerrors.New(aerrors.CodeCommandInvalid, "deployment_id is required"))
	}

	existing := a.findDeployment(p.DeploymentID)
	if existing == nil {
		// Already removed or was never tracked locally — idempotent success.
		a.logger.Info("mark draining: deployment not found in local state; already cleaned up",
			"deployment_id", p.DeploymentID)
		return a.reportSuccess(ctx, cmd.ID, cmd.DeploymentID, nil)
	}
	if existing.Status == state.DeploymentStatusDraining {
		a.logger.Info("mark draining: deployment already in draining state; nothing to do",
			"deployment_id", p.DeploymentID)
		return a.reportSuccess(ctx, cmd.ID, cmd.DeploymentID, nil)
	}

	now := time.Now().UTC()
	_ = a.modifyState(func(ls *state.LocalState) {
		if d := findDeploymentByID(ls, p.DeploymentID); d != nil {
			d.Status = state.DeploymentStatusDraining
			d.DrainingStartedAt = &now
			d.UpdatedAt = now
		}
	})

	if existing.HostPort > 0 {
		if err := a.deps.Ports.MarkDraining(existing.HostPort); err != nil {
			a.logger.Warn("mark draining: failed to mark port as draining",
				"port", existing.HostPort, "error", err)
		}
	}

	a.logger.Info("deployment marked as draining",
		"deployment_id", p.DeploymentID,
		"container_name", existing.ContainerName,
		"host_port", existing.HostPort,
	)
	return a.reportSuccess(ctx, cmd.ID, cmd.DeploymentID, nil)
}

// --- RECONCILE ---

// executeReconcile reconciles local port and deployment state against live Docker containers.
// Scope may be "ports", "deployments", or "all" (default).
func (a *RuntimeAgent) executeReconcile(ctx context.Context, cmd platform.PendingCommand) error {
	var p ReconcilePayload
	_ = json.Unmarshal(cmd.Payload, &p) // best-effort; default scope below
	if p.Scope == "" {
		p.Scope = "all"
	}

	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	containers, err := a.deps.Docker.ListContainers(listCtx, docker.ContainerFilter{ManagedOnly: true})
	cancel()
	if err != nil {
		return a.reportFailure(ctx, cmd.ID, cmd.DeploymentID,
			aerrors.Wrap(aerrors.CodeContainerListFailed, "reconcile.list", err))
	}

	live := make(map[string]bool, len(containers))
	for _, c := range containers {
		live[c.Name] = true
	}

	if p.Scope == "ports" || p.Scope == "all" {
		if _, err := a.deps.Ports.Reconcile(ctx, live); err != nil {
			a.logger.Warn("port reconciliation failed during RECONCILE command", "error", err)
		}
	}
	if p.Scope == "deployments" || p.Scope == "all" {
		a.reconcileDeployments(ctx, containers)
	}

	a.logger.Info("RECONCILE command complete",
		"scope", p.Scope,
		"live_containers", len(containers),
	)
	return a.reportSuccess(ctx, cmd.ID, cmd.DeploymentID, nil)
}

// --- report helpers ---

func (a *RuntimeAgent) reportSuccess(ctx context.Context, commandID, deploymentID string, result *platform.CommandResult) error {
	reportCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err := a.deps.Platform.ReportCommand(reportCtx, commandID, platform.CommandReportRequest{
		Status:       "succeeded",
		DeploymentID: deploymentID,
		Result:       result,
	})
	if err != nil {
		a.logger.Warn("failed to report command success",
			"command_id", commandID, "error", err)
		return err
	}
	_ = a.modifyState(func(ls *state.LocalState) {
		ls.LastSuccessfulCommandID = commandID
	})
	return nil
}

func (a *RuntimeAgent) reportFailure(ctx context.Context, commandID, deploymentID string, opErr error) error {
	code := string(aerrors.CodeOf(opErr))
	if code == "" {
		code = string(aerrors.CodeHealthCheckFailed)
	}
	reportCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err := a.deps.Platform.ReportCommand(reportCtx, commandID, platform.CommandReportRequest{
		Status:       "failed",
		DeploymentID: deploymentID,
		Error: &platform.CommandErrorPayload{
			Code:      code,
			Message:   opErr.Error(),
			Retryable: aerrors.IsRetryable(opErr),
		},
	})
	if err != nil {
		a.logger.Warn("failed to report command failure",
			"command_id", commandID, "error", err)
		return err
	}
	return nil
}

// --- state helpers (package-level, no mutex — always called within modifyState) ---

func findDeploymentByID(ls *state.LocalState, id string) *state.DeploymentEntry {
	for i := range ls.Deployments {
		if ls.Deployments[i].DeploymentID == id {
			return &ls.Deployments[i]
		}
	}
	return nil
}

func upsertDeployment(ls *state.LocalState, entry state.DeploymentEntry) {
	for i := range ls.Deployments {
		if ls.Deployments[i].DeploymentID == entry.DeploymentID {
			ls.Deployments[i] = entry
			return
		}
	}
	ls.Deployments = append(ls.Deployments, entry)
}

func removeDeploymentByID(ls *state.LocalState, id string) {
	for i := range ls.Deployments {
		if ls.Deployments[i].DeploymentID == id {
			ls.Deployments = append(ls.Deployments[:i], ls.Deployments[i+1:]...)
			return
		}
	}
}

// findDeployment is a safe concurrent read-only lookup.
func (a *RuntimeAgent) findDeployment(id string) *state.DeploymentEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.localState == nil {
		return nil
	}
	d := findDeploymentByID(a.localState, id)
	if d == nil {
		return nil
	}
	cp := *d
	return &cp
}

// --- misc helpers ---

func buildManagedLabels(commandID, deploymentID, application, environment, agentID string) map[string]string {
	return map[string]string{
		"devex.managed":       "true",
		"devex.agent_id":      agentID,
		"devex.application":   application,
		"devex.environment":   environment,
		"devex.deployment_id": deploymentID,
		"devex.command_id":    commandID,
	}
}

func healthCheckTotalTimeout(cfg health.CheckConfig) time.Duration {
	retries := cfg.Retries
	if retries < 1 {
		retries = 1
	}
	total := time.Duration(retries) *
		(time.Duration(cfg.TimeoutSeconds+cfg.IntervalSeconds+1) * time.Second)
	if total < 30*time.Second {
		total = 30 * time.Second
	}
	return total
}

func truncateID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
