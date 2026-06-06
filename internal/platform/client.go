package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"time"

	aerrors "devex-agent/internal/errors"
)

const defaultHTTPClientTimeout = 15 * time.Second

// retryPolicy controls how the client retries failed requests.
type retryPolicy struct {
	maxAttempts     int
	initialInterval time.Duration
	maxInterval     time.Duration
	multiplier      float64
	jitter          bool
}

var defaultRetryPolicy = retryPolicy{
	maxAttempts:     3,
	initialInterval: time.Second,
	maxInterval:     10 * time.Second,
	multiplier:      2.0,
	jitter:          true,
}

// Client communicates with the DevEx Platform API over HTTP.
// All calls are outbound; the agent does not expose a public API.
type Client struct {
	baseURL    string
	agentID    string
	token      string
	httpClient *http.Client
	logger     *slog.Logger
	retry      retryPolicy
}

// NewClient creates a Platform API client.
// agentID may be empty before registration; call SetAgentID after Register succeeds.
// token must never be logged.
func NewClient(baseURL, token string, logger *slog.Logger) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: defaultHTTPClientTimeout,
		},
		logger: logger,
		retry:  defaultRetryPolicy,
	}
}

// SetAgentID stores the agent ID obtained after registration.
func (c *Client) SetAgentID(id string) { c.agentID = id }

// AgentID returns the current agent ID (empty before registration).
func (c *Client) AgentID() string { return c.agentID }

// Register registers the agent with the Platform API.
// On success, call SetAgentID with the returned AgentID.
func (c *Client) Register(ctx context.Context, req RegisterRequest) (*RegisterResponse, error) {
	body, err := c.postWithRetry(ctx, "/api/agents/register", req)
	if err != nil {
		return nil, err
	}
	var resp RegisterResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, aerrors.Newf(aerrors.CodePlatformAPIError, "decode register response: %s", err)
	}
	return &resp, nil
}

// SendHeartbeat sends a heartbeat update to the Platform API.
func (c *Client) SendHeartbeat(ctx context.Context, req HeartbeatRequest) (*HeartbeatResponse, error) {
	path := fmt.Sprintf("/api/agents/%s/heartbeat", c.agentID)
	body, err := c.postWithRetry(ctx, path, req)
	if err != nil {
		return nil, err
	}
	var resp HeartbeatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, aerrors.Newf(aerrors.CodePlatformAPIError, "decode heartbeat response: %s", err)
	}
	return &resp, nil
}

// FetchPendingCommands retrieves commands pending for this agent.
// Returns an empty slice (not an error) when there are no pending commands.
func (c *Client) FetchPendingCommands(ctx context.Context) ([]PendingCommand, error) {
	path := fmt.Sprintf("/api/agents/%s/commands/pending", c.agentID)
	body, err := c.getWithRetry(ctx, path)
	if err != nil {
		return nil, err
	}
	var cmds []PendingCommand
	if err := json.Unmarshal(body, &cmds); err != nil {
		return nil, aerrors.Newf(aerrors.CodePlatformAPIError, "decode pending commands: %s", err)
	}
	return cmds, nil
}

// ClaimCommand atomically claims a pending command.
// Returns CodeCommandClaimFailed when the command is no longer claimable (HTTP 409).
// The agent must NOT execute a command that was not successfully claimed.
func (c *Client) ClaimCommand(ctx context.Context, commandID string) (*ClaimResponse, error) {
	path := fmt.Sprintf("/api/agents/%s/commands/%s/claim", c.agentID, commandID)
	req := map[string]string{"status": "claimed"}
	body, err := c.postWithRetry(ctx, path, req)
	if err != nil {
		return nil, err
	}
	var resp ClaimResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, aerrors.Newf(aerrors.CodePlatformAPIError, "decode claim response: %s", err)
	}
	return &resp, nil
}

// StartCommand marks a claimed command as running.
func (c *Client) StartCommand(ctx context.Context, commandID string) (*StartResponse, error) {
	path := fmt.Sprintf("/api/agents/%s/commands/%s/start", c.agentID, commandID)
	req := map[string]string{"status": "running"}
	body, err := c.postWithRetry(ctx, path, req)
	if err != nil {
		return nil, err
	}
	var resp StartResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, aerrors.Newf(aerrors.CodePlatformAPIError, "decode start response: %s", err)
	}
	return &resp, nil
}

// ReportCommand reports the final outcome of a command.
// Use status "succeeded" with a Result, or "failed" with an Error.
func (c *Client) ReportCommand(ctx context.Context, commandID string, req CommandReportRequest) error {
	path := fmt.Sprintf("/api/agents/%s/commands/%s/report", c.agentID, commandID)
	_, err := c.postWithRetry(ctx, path, req)
	return err
}

// FetchDesiredState retrieves the current desired state for this agent.
func (c *Client) FetchDesiredState(ctx context.Context) (*DesiredState, error) {
	path := fmt.Sprintf("/api/agents/%s/desired-state", c.agentID)
	body, err := c.getWithRetry(ctx, path)
	if err != nil {
		return nil, err
	}
	var state DesiredState
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, aerrors.Newf(aerrors.CodePlatformAPIError, "decode desired state: %s", err)
	}
	return &state, nil
}

// ReportDesiredState reports the result of applying a desired state.
func (c *Client) ReportDesiredState(ctx context.Context, req DesiredStateReportRequest) error {
	path := fmt.Sprintf("/api/agents/%s/desired-state/report", c.agentID)
	_, err := c.postWithRetry(ctx, path, req)
	return err
}

// --- internal helpers ---

func (c *Client) getWithRetry(ctx context.Context, path string) ([]byte, error) {
	return c.doWithRetry(ctx, http.MethodGet, path, nil)
}

func (c *Client) postWithRetry(ctx context.Context, path string, body any) ([]byte, error) {
	return c.doWithRetry(ctx, http.MethodPost, path, body)
}

// doWithRetry executes an HTTP request and retries on transient failures using
// exponential backoff with optional jitter.
func (c *Client) doWithRetry(ctx context.Context, method, path string, body any) ([]byte, error) {
	var lastErr error
	interval := c.retry.initialInterval

	for attempt := 1; attempt <= c.retry.maxAttempts; attempt++ {
		respBody, statusCode, err := c.do(ctx, method, path, body)
		if err == nil {
			return respBody, nil
		}

		if !retryableStatus(statusCode) {
			return nil, err
		}

		lastErr = err
		if attempt == c.retry.maxAttempts {
			break
		}

		wait := interval
		if c.retry.jitter && interval > 0 {
			jitterRange := int64(interval) / 5
			if jitterRange > 0 {
				wait += time.Duration(rand.Int63n(jitterRange))
			}
		}
		if wait > c.retry.maxInterval {
			wait = c.retry.maxInterval
		}

		c.logger.Debug("retrying platform request",
			"method", method,
			"path", path,
			"attempt", attempt,
			"wait_ms", wait.Milliseconds(),
		)

		if wait > 0 {
			select {
			case <-ctx.Done():
				return nil, aerrors.Wrap(aerrors.CodeCommandTimeout, "platform.retry", ctx.Err())
			case <-time.After(wait):
			}
		}

		interval = time.Duration(float64(interval) * c.retry.multiplier)
		if interval > c.retry.maxInterval {
			interval = c.retry.maxInterval
		}
	}
	return nil, lastErr
}

// do performs a single HTTP request.
// Returns (responseBody, statusCode, error); statusCode is 0 on network/transport errors.
func (c *Client) do(ctx context.Context, method, path string, bodyObj any) ([]byte, int, error) {
	var reqBody io.Reader
	if bodyObj != nil {
		data, err := json.Marshal(bodyObj)
		if err != nil {
			return nil, 0, aerrors.Newf(aerrors.CodePlatformAPIError, "marshal request body: %s", err)
		}
		reqBody = bytes.NewReader(data)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, 0, aerrors.Newf(aerrors.CodePlatformAPIError, "create HTTP request: %s", err)
	}

	// Token is set in the header but never logged.
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if bodyObj != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Treat context cancellation as non-retryable command timeout.
		if ctx.Err() != nil {
			return nil, 0, aerrors.Wrap(aerrors.CodeCommandTimeout, "platform.http", ctx.Err())
		}
		// Network/transport error → retryable.
		return nil, 0, aerrors.WrapRetryable(aerrors.CodePlatformAPIUnavailable, "platform.http", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, aerrors.Newf(aerrors.CodePlatformAPIError,
			"read response body (HTTP %d): %s", resp.StatusCode, err)
	}

	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, c.buildAPIError(respBody, resp.StatusCode)
	}

	return respBody, resp.StatusCode, nil
}

// buildAPIError converts an error HTTP response into a typed OperationalError.
func (c *Client) buildAPIError(body []byte, statusCode int) error {
	var envelope apiErrorEnvelope
	_ = json.Unmarshal(body, &envelope) // best-effort; fall back to generic message

	msg := envelope.Error.Message
	if msg == "" {
		msg = fmt.Sprintf("platform API returned HTTP %d", statusCode)
	}

	code := httpStatusToErrorCode(statusCode)
	if retryableStatus(statusCode) {
		return aerrors.NewRetryable(code, msg)
	}
	return aerrors.New(code, msg)
}

// httpStatusToErrorCode maps HTTP status codes to typed error codes.
func httpStatusToErrorCode(status int) aerrors.ErrorCode {
	switch status {
	case http.StatusUnauthorized:
		return aerrors.CodeAuthenticationFailed
	case http.StatusForbidden:
		return aerrors.CodeAuthorizationFailed
	case http.StatusConflict:
		return aerrors.CodeCommandClaimFailed
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return aerrors.CodeCommandInvalid
	case http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return aerrors.CodePlatformAPIUnavailable
	default:
		return aerrors.CodePlatformAPIError
	}
}

// retryableStatus returns true for HTTP status codes and network errors that
// warrant a retry attempt.
// statusCode == 0 means a transport/network error occurred.
func retryableStatus(statusCode int) bool {
	switch statusCode {
	case 0,
		http.StatusRequestTimeout,      // 408
		http.StatusTooManyRequests,      // 429
		http.StatusInternalServerError,  // 500
		http.StatusBadGateway,           // 502
		http.StatusServiceUnavailable,   // 503
		http.StatusGatewayTimeout:       // 504
		return true
	}
	return false
}
