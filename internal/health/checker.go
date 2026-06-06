package health

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	aerrors "devex-agent/internal/errors"
)

// Checker performs health checks for containers, HTTP endpoints, TCP ports,
// and gateway routes.
type Checker interface {
	CheckHTTP(ctx context.Context, target HTTPCheckTarget) (*Result, error)
	CheckTCP(ctx context.Context, target TCPCheckTarget) (*Result, error)
	CheckContainer(ctx context.Context, status ContainerStatus) (*Result, error)
}

// DefaultChecker is the production implementation of Checker.
type DefaultChecker struct {
	httpClient *http.Client
	logger     *slog.Logger
}

// NewDefaultChecker creates a DefaultChecker with a standard HTTP client.
// The client does not follow redirects by default (per spec).
func NewDefaultChecker(logger *slog.Logger) *DefaultChecker {
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &DefaultChecker{httpClient: client, logger: logger}
}

// newWithClient creates a DefaultChecker with a custom HTTP client (for testing).
func newWithClient(client *http.Client, logger *slog.Logger) *DefaultChecker {
	return &DefaultChecker{httpClient: client, logger: logger}
}

// --- CheckHTTP ---

// CheckHTTP performs an HTTP health check against target.URL.
// It retries up to target.Config.Retries times with a fixed interval between attempts.
// If target.Host is set, it is used as the HTTP Host header (gateway route checks).
func (c *DefaultChecker) CheckHTTP(ctx context.Context, target HTTPCheckTarget) (*Result, error) {
	checkType := target.CheckType
	if checkType == "" {
		checkType = TypeHTTP
	}

	cfg := applyHTTPDefaults(target.Config)
	start := time.Now()
	maxAttempts := cfg.Retries
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	c.logger.Info("health_check_started",
		"type", checkType,
		"target", target.URL,
		"retries", maxAttempts,
	)

	var (
		lastCode    aerrors.ErrorCode
		lastMessage string
		lastStatus  int
	)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		statusCode, err := c.doHTTPAttempt(ctx, target, cfg)

		if err == nil {
			dur := time.Since(start)
			c.logger.Info("health_check_succeeded",
				"type", checkType,
				"target", target.URL,
				"attempt", attempt,
				"status_code", statusCode,
				"duration_ms", dur.Milliseconds(),
			)
			return &Result{
				Status:     StatusHealthy,
				Type:       checkType,
				Target:     target.URL,
				Attempts:   attempt,
				Duration:   dur,
				StatusCode: statusCode,
			}, nil
		}

		lastCode = aerrors.CodeOf(err)
		lastMessage = err.Error()
		lastStatus = statusCode

		c.logger.Info("health_check_attempt_failed",
			"type", checkType,
			"target", target.URL,
			"attempt", attempt,
			"max_attempts", maxAttempts,
			"error_code", lastCode,
		)

		if attempt < maxAttempts {
			c.sleep(ctx, time.Duration(cfg.IntervalSeconds)*time.Second)
			if ctx.Err() != nil {
				break
			}
		}
	}

	dur := time.Since(start)
	c.logger.Info("health_check_failed",
		"type", checkType,
		"target", target.URL,
		"attempts", maxAttempts,
		"error_code", lastCode,
		"duration_ms", dur.Milliseconds(),
	)

	result := &Result{
		Status:       StatusUnhealthy,
		Type:         checkType,
		Target:       target.URL,
		Attempts:     maxAttempts,
		Duration:     dur,
		StatusCode:   lastStatus,
		ErrorCode:    string(lastCode),
		ErrorMessage: lastMessage,
	}
	return result, aerrors.New(lastCode, lastMessage)
}

// doHTTPAttempt performs a single HTTP request and returns (statusCode, error).
// error is nil on success (2xx, or 3xx if Allow3xx).
func (c *DefaultChecker) doHTTPAttempt(ctx context.Context, target HTTPCheckTarget, cfg CheckConfig) (int, error) {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, target.URL, nil)
	if err != nil {
		return 0, aerrors.Newf(aerrors.CodeHealthCheckInvalidResponse,
			"create request for %q: %s", target.URL, err)
	}
	if target.Host != "" {
		req.Host = target.Host
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, classifyHTTPError(err, target.URL)
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	if code >= 200 && code < 300 {
		return code, nil
	}
	if cfg.Allow3xx && code >= 300 && code < 400 {
		return code, nil
	}
	return code, aerrors.Newf(aerrors.CodeHealthCheckUnexpectedStatus,
		"HTTP %d from %q", code, target.URL)
}

// classifyHTTPError maps a net/http error to a typed health check error.
func classifyHTTPError(err error, url string) error {
	msg := err.Error()
	switch {
	case isContextTimeout(err) || strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "context deadline"):
		return aerrors.Newf(aerrors.CodeHealthCheckTimeout, "timeout reaching %q: %s", url, err)
	case strings.Contains(msg, "connection refused"):
		return aerrors.Newf(aerrors.CodeHealthCheckConnectionRefused, "connection refused at %q", url)
	default:
		return aerrors.Newf(aerrors.CodeHealthCheckFailed, "HTTP request to %q failed: %s", url, err)
	}
}

func isContextTimeout(err error) bool {
	if err == nil {
		return false
	}
	netErr, ok := err.(net.Error)
	return ok && netErr.Timeout()
}

// --- CheckTCP ---

// CheckTCP verifies that target.Address accepts TCP connections.
func (c *DefaultChecker) CheckTCP(ctx context.Context, target TCPCheckTarget) (*Result, error) {
	cfg := applyHTTPDefaults(target.Config)
	start := time.Now()
	maxAttempts := cfg.Retries
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	url := "tcp://" + target.Address

	c.logger.Info("health_check_started", "type", TypeTCP, "target", url, "retries", maxAttempts)

	var (
		lastCode    aerrors.ErrorCode
		lastMessage string
	)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		conn, err := (&net.Dialer{}).DialContext(attemptCtx, "tcp", target.Address)
		cancel()

		if err == nil {
			conn.Close()
			dur := time.Since(start)
			c.logger.Info("health_check_succeeded", "type", TypeTCP, "target", url, "attempt", attempt, "duration_ms", dur.Milliseconds())
			return &Result{
				Status:   StatusHealthy,
				Type:     TypeTCP,
				Target:   url,
				Attempts: attempt,
				Duration: dur,
			}, nil
		}

		switch {
		case strings.Contains(err.Error(), "connection refused"):
			lastCode = aerrors.CodeHealthCheckConnectionRefused
		default:
			lastCode = aerrors.CodeHealthCheckFailed
		}
		lastMessage = fmt.Sprintf("TCP connect to %q: %s", target.Address, err)

		c.logger.Info("health_check_attempt_failed", "type", TypeTCP, "target", url, "attempt", attempt, "error_code", lastCode)

		if attempt < maxAttempts {
			c.sleep(ctx, time.Duration(cfg.IntervalSeconds)*time.Second)
			if ctx.Err() != nil {
				break
			}
		}
	}

	dur := time.Since(start)
	c.logger.Info("health_check_failed", "type", TypeTCP, "target", url, "attempts", maxAttempts, "error_code", lastCode, "duration_ms", dur.Milliseconds())

	result := &Result{
		Status:       StatusUnhealthy,
		Type:         TypeTCP,
		Target:       url,
		Attempts:     maxAttempts,
		Duration:     dur,
		ErrorCode:    string(lastCode),
		ErrorMessage: lastMessage,
	}
	return result, aerrors.New(lastCode, lastMessage)
}

// --- CheckContainer ---

// CheckContainer validates that a container is running and healthy.
// It inspects the ContainerStatus fields directly — no network I/O.
// Callers should convert docker.ContainerInfo to ContainerStatus before calling.
func (c *DefaultChecker) CheckContainer(_ context.Context, status ContainerStatus) (*Result, error) {
	target := "container://" + status.Name

	unhealthy := func(code aerrors.ErrorCode, msg string) (*Result, error) {
		c.logger.Info("health_check_failed",
			"type", TypeContainer,
			"target", target,
			"container_name", status.Name,
			"error_code", code,
		)
		return &Result{
			Status:       StatusUnhealthy,
			Type:         TypeContainer,
			Target:       target,
			Attempts:     1,
			ErrorCode:    string(code),
			ErrorMessage: msg,
		}, aerrors.New(code, msg)
	}

	if !status.Running {
		msg := fmt.Sprintf("container %q is not running (status=%q, exit_code=%d)",
			status.Name, status.Status, status.ExitCode)
		return unhealthy(aerrors.CodeHealthCheckContainerNotRunning, msg)
	}

	s := strings.ToLower(status.Status)
	if s == "exited" || s == "dead" || s == "removing" {
		msg := fmt.Sprintf("container %q is in terminal state %q (exit_code=%d)",
			status.Name, status.Status, status.ExitCode)
		return unhealthy(aerrors.CodeHealthCheckContainerNotRunning, msg)
	}

	c.logger.Info("health_check_succeeded",
		"type", TypeContainer,
		"target", target,
		"container_name", status.Name,
	)
	return &Result{
		Status:   StatusHealthy,
		Type:     TypeContainer,
		Target:   target,
		Attempts: 1,
	}, nil
}

// --- helpers ---

func (c *DefaultChecker) sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// applyHTTPDefaults fills in zero-value CheckConfig fields with sensible defaults.
func applyHTTPDefaults(cfg CheckConfig) CheckConfig {
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 2
	}
	if cfg.Retries <= 0 {
		cfg.Retries = 1
	}
	// IntervalSeconds 0 is allowed (no wait between retries, useful in tests).
	return cfg
}
