package agent

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	aerrors "devex-agent/internal/errors"
	"devex-agent/internal/config"
	"devex-agent/internal/health"
	"devex-agent/internal/platform"
	"devex-agent/internal/state"
)

// GatewayPlatformClient is the Platform API interface needed by the Gateway Agent.
type GatewayPlatformClient interface {
	SetAgentID(id string)
	Register(ctx context.Context, req platform.RegisterRequest) (*platform.RegisterResponse, error)
	SendHeartbeat(ctx context.Context, req platform.HeartbeatRequest) (*platform.HeartbeatResponse, error)
	FetchDesiredState(ctx context.Context) (*platform.DesiredState, error)
	ReportDesiredState(ctx context.Context, req platform.DesiredStateReportRequest) error
}

// CaddyAdminClient applies and persists Caddy configurations.
type CaddyAdminClient interface {
	Load(ctx context.Context, configJSON []byte) error
	Ping(ctx context.Context) error
	SaveConfig(path string, data []byte) error
	LoadConfig(path string) ([]byte, error)
}

// CaddyConfigGenerator produces Caddy JSON from desired routes.
type CaddyConfigGenerator interface {
	GenerateJSON(routes []platform.DesiredRoute) ([]byte, error)
	EmergencyConfigJSON() ([]byte, error)
}

// GatewayHealthChecker performs HTTP health checks for route validation.
type GatewayHealthChecker interface {
	CheckHTTP(ctx context.Context, target health.HTTPCheckTarget) (*health.Result, error)
}

// GatewayDependencies holds all external dependencies injected into GatewayAgent.
type GatewayDependencies struct {
	Platform  GatewayPlatformClient
	Caddy     CaddyAdminClient
	Generator CaddyConfigGenerator
	State     StateStore
	Health    GatewayHealthChecker
}

// GatewayAgent reconciles Caddy routing configuration with the DevEx Platform desired state.
type GatewayAgent struct {
	cfg    *config.Config
	deps   GatewayDependencies
	logger *slog.Logger

	privateIP  string
	hostname   string
	instanceID string

	mu                    sync.Mutex
	agentID               string
	lastSuccessfulVersion int
	lastAttemptedVersion  int
	routesTotal           int
	caddyStatus           string
}

// NewGateway creates a GatewayAgent.
func NewGateway(cfg *config.Config, deps GatewayDependencies, logger *slog.Logger) *GatewayAgent {
	return &GatewayAgent{
		cfg:         cfg,
		deps:        deps,
		logger:      logger,
		caddyStatus: "unknown",
	}
}

// Run is the main entry point for the Gateway Agent. Blocks until ctx is cancelled.
// Boot sequence: discover host info → register → ping Caddy → heartbeat loop → reconcile loop.
func (a *GatewayAgent) Run(ctx context.Context) error {
	a.hostname = discoverHostname()
	a.instanceID = discoverInstanceID()
	a.privateIP = discoverPrivateIP()

	a.logger.Info("gateway-agent starting",
		"version", AgentVersion,
		"hostname", a.hostname,
		"private_ip", a.privateIP,
		"environment", a.cfg.Agent.Environment,
	)

	if err := a.ensureRegistered(ctx); err != nil {
		return fmt.Errorf("gateway registration: %w", err)
	}

	// Ping Caddy at startup; log warning but don't abort.
	if err := a.pingCaddy(ctx); err != nil {
		a.logger.Warn("gateway: caddy admin api not reachable at startup; will retry in reconcile loop",
			"error", err)
	}

	go a.heartbeatLoop(ctx)
	a.reconcileLoop(ctx)
	return nil
}

// ensureRegistered loads persisted agent identity or registers with the Platform API.
func (a *GatewayAgent) ensureRegistered(ctx context.Context) error {
	identity, err := a.deps.State.LoadAgentIdentity()
	if err != nil {
		return err
	}
	if identity != nil && identity.AgentID != "" {
		a.mu.Lock()
		a.agentID = identity.AgentID
		a.mu.Unlock()
		a.deps.Platform.SetAgentID(identity.AgentID)
		a.logger.Info("gateway identity loaded from disk", "agent_id", identity.AgentID)
		return nil
	}

	req := platform.RegisterRequest{
		Mode:         a.cfg.Agent.Mode,
		Environment:  a.cfg.Agent.Environment,
		Role:         a.cfg.Agent.Role,
		Hostname:     a.hostname,
		InstanceID:   a.instanceID,
		PrivateIP:    a.privateIP,
		Version:      AgentVersion,
		Capabilities: platform.NewGatewayCapabilities(a.cfg.Caddy.AdminURL),
	}

	resp, err := a.deps.Platform.Register(ctx, req)
	if err != nil {
		return err
	}

	a.mu.Lock()
	a.agentID = resp.AgentID
	a.mu.Unlock()
	a.deps.Platform.SetAgentID(resp.AgentID)

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
	if saveErr := a.deps.State.SaveAgentIdentity(newIdentity); saveErr != nil {
		a.logger.Warn("gateway: failed to persist identity; will re-register on next restart",
			"error", saveErr)
	}
	a.logger.Info("gateway registered with platform",
		"agent_id", resp.AgentID,
		"status", resp.Status,
	)
	return nil
}

// pingCaddy checks if the Caddy Admin API is reachable and updates caddyStatus.
func (a *GatewayAgent) pingCaddy(ctx context.Context) error {
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := a.deps.Caddy.Ping(pingCtx)
	a.mu.Lock()
	if err != nil {
		a.caddyStatus = "unhealthy"
	} else {
		a.caddyStatus = "healthy"
	}
	a.mu.Unlock()
	return err
}

// heartbeatLoop sends a heartbeat immediately, then on a fixed interval.
func (a *GatewayAgent) heartbeatLoop(ctx context.Context) {
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

// sendHeartbeat sends a single heartbeat to the Platform API.
func (a *GatewayAgent) sendHeartbeat(ctx context.Context) {
	_ = a.pingCaddy(ctx)

	a.mu.Lock()
	lastApplied := a.lastAttemptedVersion
	lastSuccessful := a.lastSuccessfulVersion
	total := a.routesTotal
	caddyStatus := a.caddyStatus
	a.mu.Unlock()

	req := platform.HeartbeatRequest{
		Status:                            "online",
		Mode:                              a.cfg.Agent.Mode,
		Environment:                       a.cfg.Agent.Environment,
		Role:                              a.cfg.Agent.Role,
		Version:                           AgentVersion,
		PrivateIP:                         a.privateIP,
		CaddyStatus:                       caddyStatus,
		RoutesTotal:                       total,
		LastAppliedDesiredStateVersion:    lastApplied,
		LastSuccessfulDesiredStateVersion: lastSuccessful,
	}

	hbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := a.deps.Platform.SendHeartbeat(hbCtx, req); err != nil {
		a.logger.Warn("gateway heartbeat failed", "error", err)
	}
}

// reconcileLoop runs reconcileOnce on every interval until ctx is cancelled.
func (a *GatewayAgent) reconcileLoop(ctx context.Context) {
	interval := time.Duration(a.cfg.Reconcile.IntervalSecs) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}

	for {
		select {
		case <-ctx.Done():
			a.logger.Info("gateway reconcile loop stopping")
			return
		default:
		}

		if err := a.reconcileOnce(ctx); err != nil {
			a.logger.Warn("gateway reconcile error", "error", err)
		}

		jitter := time.Duration(rand.Int64N(int64(interval/10) + 1))
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval + jitter):
		}
	}
}

// reconcileOnce fetches desired state and applies it if the version changed.
func (a *GatewayAgent) reconcileOnce(ctx context.Context) error {
	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	ds, err := a.deps.Platform.FetchDesiredState(fetchCtx)
	cancel()
	if err != nil {
		a.logger.Warn("gateway: failed to fetch desired state", "error", err)
		return nil // non-fatal: will retry next cycle
	}

	a.logger.Debug("gateway desired state fetched",
		"version", ds.Version,
		"routes", len(ds.Routes),
	)

	a.mu.Lock()
	lastSuccessful := a.lastSuccessfulVersion
	a.mu.Unlock()

	if ds.Version == lastSuccessful {
		a.logger.Debug("gateway desired state unchanged", "version", ds.Version)
		return nil
	}

	a.mu.Lock()
	a.lastAttemptedVersion = ds.Version
	a.mu.Unlock()

	return a.applyDesiredState(ctx, ds)
}

// applyDesiredState generates, loads, and validates a new Caddy configuration.
func (a *GatewayAgent) applyDesiredState(ctx context.Context, ds *platform.DesiredState) error {
	a.logger.Info("gateway: applying desired state",
		"version", ds.Version,
		"routes", len(ds.Routes),
	)

	configJSON, err := a.deps.Generator.GenerateJSON(ds.Routes)
	if err != nil {
		a.logger.Error("gateway: config generation failed", "version", ds.Version, "error", err)
		a.reportFailure(ctx, ds.Version, aerrors.CodeCaddyConfigGenerationFailed, err.Error())
		return nil
	}
	a.logger.Debug("gateway: caddy config generated", "version", ds.Version)

	// Save previous config (copy current → previous; best-effort).
	if prevPath := a.cfg.Caddy.PreviousConfigPath; prevPath != "" {
		if cur, loadErr := a.deps.Caddy.LoadConfig(a.cfg.Caddy.CurrentConfigPath); loadErr == nil && cur != nil {
			if saveErr := a.deps.Caddy.SaveConfig(prevPath, cur); saveErr != nil {
				a.logger.Warn("gateway: failed to save previous config", "error", saveErr)
			}
		}
	}

	// Save current candidate config (best-effort).
	if curPath := a.cfg.Caddy.CurrentConfigPath; curPath != "" {
		if saveErr := a.deps.Caddy.SaveConfig(curPath, configJSON); saveErr != nil {
			a.logger.Warn("gateway: failed to save current config", "error", saveErr)
		}
	}

	// Apply via Caddy /load.
	loadTimeout := time.Duration(a.cfg.Caddy.LoadTimeoutSecs) * time.Second
	if loadTimeout <= 0 {
		loadTimeout = 10 * time.Second
	}
	loadCtx, loadCancel := context.WithTimeout(ctx, loadTimeout)
	loadErr := a.deps.Caddy.Load(loadCtx, configJSON)
	loadCancel()

	if loadErr != nil {
		a.logger.Error("gateway: caddy /load failed", "version", ds.Version, "error", loadErr)
		a.restoreLastGood(ctx)
		a.reportFailure(ctx, ds.Version, aerrors.CodeCaddyLoadFailed, loadErr.Error())
		return nil
	}
	a.logger.Info("gateway: caddy /load succeeded", "version", ds.Version)

	// Validate routes.
	validated, failedHosts := a.validateRoutes(ctx, ds.Routes)
	total := len(ds.Routes)
	failedCount := len(failedHosts)

	if failedCount > 0 {
		errMsg := fmt.Sprintf("%d/%d routes failed validation: %s",
			failedCount, total, failedHosts[0])
		a.logger.Error("gateway: route validation failed",
			"version", ds.Version,
			"failed", failedCount,
			"total", total,
		)
		a.restoreLastGood(ctx)
		a.reportFailure(ctx, ds.Version, aerrors.CodeCaddyRouteValidationFailed, errMsg)
		return nil
	}

	// Promote candidate to last-good.
	if lgPath := a.cfg.Caddy.LastGoodConfigPath; lgPath != "" {
		if saveErr := a.deps.Caddy.SaveConfig(lgPath, configJSON); saveErr != nil {
			a.logger.Warn("gateway: failed to promote last-good config", "error", saveErr)
		}
	}

	a.mu.Lock()
	a.lastSuccessfulVersion = ds.Version
	a.routesTotal = total
	a.mu.Unlock()

	a.logger.Info("gateway: desired state applied successfully",
		"version", ds.Version,
		"routes_total", total,
		"validated_routes", validated,
	)

	a.reportSuccess(ctx, ds.Version, total, validated)
	return nil
}

// validateRoutes performs HTTP health checks for each route that has a health_check_path.
// Returns (validated count, list of failed route host names).
func (a *GatewayAgent) validateRoutes(ctx context.Context, routes []platform.DesiredRoute) (int, []string) {
	validationTimeout := time.Duration(a.cfg.Caddy.RouteValidationTimeoutSecs) * time.Second
	if validationTimeout <= 0 {
		validationTimeout = 3 * time.Second
	}

	validated := 0
	var failed []string

	for _, r := range routes {
		if r.HealthCheckPath == "" {
			validated++
			continue
		}

		url := "http://127.0.0.1" + r.HealthCheckPath
		target := health.HTTPCheckTarget{
			URL:       url,
			Host:      r.Host,
			CheckType: health.TypeGatewayRoute,
			Config: health.CheckConfig{
				TimeoutSeconds: int(validationTimeout.Seconds()),
				Retries:        1,
			},
		}

		checkCtx, cancel := context.WithTimeout(ctx, validationTimeout+2*time.Second)
		result, err := a.deps.Health.CheckHTTP(checkCtx, target)
		cancel()

		if err != nil || (result != nil && !result.Healthy()) {
			hostErr := r.Host
			if err != nil {
				hostErr = fmt.Sprintf("%s: %s", r.Host, err.Error())
			}
			failed = append(failed, hostErr)
			a.logger.Warn("gateway: route validation failed",
				"host", r.Host,
				"upstream", r.Upstream,
				"error", err,
			)
		} else {
			validated++
			a.logger.Debug("gateway: route validated", "host", r.Host, "upstream", r.Upstream)
		}
	}
	return validated, failed
}

// restoreLastGood attempts to re-apply the last-good Caddy configuration.
func (a *GatewayAgent) restoreLastGood(ctx context.Context) {
	lgPath := a.cfg.Caddy.LastGoodConfigPath
	if lgPath == "" {
		a.logger.Warn("gateway: no last-good config path configured; cannot restore")
		return
	}

	lastGood, err := a.deps.Caddy.LoadConfig(lgPath)
	if err != nil {
		a.logger.Error("gateway: failed to read last-good config",
			"error", err,
			"code", string(aerrors.CodeCaddyLastGoodRestoreFailed))
		return
	}
	if lastGood == nil {
		a.logger.Warn("gateway: no last-good config available; cannot restore")
		return
	}

	loadCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.deps.Caddy.Load(loadCtx, lastGood); err != nil {
		a.logger.Error("gateway: last-good restore failed",
			"error", err,
			"code", string(aerrors.CodeCaddyLastGoodRestoreFailed))
		return
	}
	a.logger.Info("gateway: last-good config restored")
}

// reportSuccess reports a successfully applied desired state to the Platform API.
func (a *GatewayAgent) reportSuccess(ctx context.Context, version, total, validated int) {
	now := time.Now().UTC()
	req := platform.DesiredStateReportRequest{
		Status:              "applied",
		DesiredStateVersion: version,
		Type:                "gateway_routes",
		RoutesTotal:         total,
		ValidatedRoutes:     validated,
		FailedRoutes:        0,
		AppliedAt:           &now,
	}
	rCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.deps.Platform.ReportDesiredState(rCtx, req); err != nil {
		a.logger.Warn("gateway: failed to report desired state success", "error", err)
	}
}

// reportFailure reports a failed desired state application to the Platform API.
func (a *GatewayAgent) reportFailure(ctx context.Context, version int, code aerrors.ErrorCode, msg string) {
	req := platform.DesiredStateReportRequest{
		Status:              "failed",
		DesiredStateVersion: version,
		Type:                "gateway_routes",
		Error: &platform.CommandErrorPayload{
			Code:    string(code),
			Message: msg,
		},
	}
	rCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.deps.Platform.ReportDesiredState(rCtx, req); err != nil {
		a.logger.Warn("gateway: failed to report desired state failure", "error", err)
	}
}
