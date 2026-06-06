package caddy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	aerrors "devex-agent/internal/errors"
)

// Client wraps the Caddy Admin API.
// The Admin API must be accessible only locally (e.g. http://127.0.0.1:2019).
type Client struct {
	adminURL   string
	httpClient *http.Client
	logger     *slog.Logger
}

// NewClient creates a Caddy Admin API client.
// adminURL must point to the local Admin API, e.g. "http://127.0.0.1:2019".
func NewClient(adminURL string, logger *slog.Logger) *Client {
	return &Client{
		adminURL:   adminURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
	}
}

// newClientWithHTTP creates a client with a custom http.Client (used in tests).
func newClientWithHTTP(adminURL string, httpClient *http.Client, logger *slog.Logger) *Client {
	return &Client{adminURL: adminURL, httpClient: httpClient, logger: logger}
}

// Load applies a complete Caddy configuration by POST-ing to /load.
// Caddy applies the configuration atomically; on success the previous config is replaced.
func (c *Client) Load(ctx context.Context, configJSON []byte) error {
	url := c.adminURL + "/load"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(configJSON))
	if err != nil {
		return aerrors.Newf(aerrors.CodeCaddyLoadFailed, "build /load request: %s", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return aerrors.Wrap(aerrors.CodeCaddyAdminUnavailable, "caddy.load", ctx.Err())
		}
		return aerrors.WrapRetryable(aerrors.CodeCaddyAdminUnavailable, "caddy.load", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return aerrors.Newf(aerrors.CodeCaddyLoadFailed,
			"caddy /load returned HTTP %d: %s", resp.StatusCode, trimBody(body))
	}
	return nil
}

// Ping checks that the Caddy Admin API is reachable by calling GET /config.
// A 200–4xx response is considered "reachable"; only 5xx or network errors fail.
func (c *Client) Ping(ctx context.Context) error {
	url := c.adminURL + "/config"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return aerrors.Newf(aerrors.CodeCaddyAdminUnavailable, "build /config request: %s", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return aerrors.Wrap(aerrors.CodeCaddyAdminUnavailable, "caddy.ping", ctx.Err())
		}
		return aerrors.WrapRetryable(aerrors.CodeCaddyAdminUnavailable, "caddy.ping", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return aerrors.Newf(aerrors.CodeCaddyAdminUnavailable,
			"caddy admin returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// SaveConfig writes data to path using an atomic write (write-to-temp, then rename).
// Parent directories are created if they do not exist.
func (c *Client) SaveConfig(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return aerrors.Newf(aerrors.CodeStateWriteFailed,
			"create config directory %s: %s", dir, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return aerrors.Newf(aerrors.CodeStateWriteFailed,
			"write temp config %s: %s", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return aerrors.Newf(aerrors.CodeStateWriteFailed,
			"rename %s to %s: %s", tmp, path, err)
	}
	return nil
}

// LoadConfig reads config bytes from path.
// Returns (nil, nil) when the file does not exist (not an error — no prior config).
func (c *Client) LoadConfig(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, aerrors.Newf(aerrors.CodeStateLoadFailed, "read config %s: %s", path, err)
	}
	return data, nil
}

// trimBody returns up to 200 bytes of body as a string for error messages.
func trimBody(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "..."
	}
	return string(b)
}
