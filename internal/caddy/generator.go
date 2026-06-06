package caddy

import (
	"encoding/json"
	"net"
	"regexp"
	"sort"
	"strings"

	aerrors "devex-agent/internal/errors"
	"devex-agent/internal/platform"
)

const adminListenAddress = "0.0.0.0:2019"

// defaultListenAddresses are the ports the Caddy HTTP server listens on.
var defaultListenAddresses = []string{":80", ":443"}

// blockedUpstreamHosts must never appear in an upstream dial address.
var blockedUpstreamHosts = map[string]bool{
	"169.254.169.254": true, // AWS EC2 metadata service — always block
	"127.0.0.1":       true,
	"localhost":        true,
	"0.0.0.0":         true,
	"::1":             true,
}

// hostPattern permits letters, digits, hyphens and dots; requires at least
// two characters so a single dot does not pass.
var hostPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9\-\.]*[a-zA-Z0-9]$`)

// Generator builds complete Caddy configurations from desired routes.
// Configurations are generated deterministically (routes sorted by host).
type Generator struct {
	listenAddresses []string
}

// NewGenerator creates a Generator.
// Pass nil to use the default listen addresses [:80, :443].
func NewGenerator(listen []string) *Generator {
	if len(listen) == 0 {
		listen = defaultListenAddresses
	}
	return &Generator{listenAddresses: listen}
}

// Generate builds a CaddyConfig from routes.
// Returns an error with CodeInvalidHost or CodeInvalidUpstream when a route
// fails validation, or CodeCaddyConfigGenerationFailed for internal errors.
func (g *Generator) Generate(routes []platform.DesiredRoute) (*CaddyConfig, error) {
	sorted := make([]platform.DesiredRoute, len(routes))
	copy(sorted, routes)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Host < sorted[j].Host
	})

	caddyRoutes := make([]*Route, 0, len(sorted))
	for _, r := range sorted {
		if err := ValidateHost(r.Host); err != nil {
			return nil, err
		}
		if err := ValidateUpstream(r.Upstream); err != nil {
			return nil, err
		}
		caddyRoutes = append(caddyRoutes, &Route{
			Match:  []MatchConfig{{Host: []string{r.Host}}},
			Handle: []Handler{{Handler: "reverse_proxy", Upstreams: []Upstream{{Dial: r.Upstream}}}},
		})
	}

	return &CaddyConfig{
		Admin: &AdminConfig{Listen: adminListenAddress},
		Apps: &AppsConfig{
			HTTP: &HTTPApp{
				Servers: map[string]*HTTPServer{
					"srv0": {
						Listen: g.listenAddresses,
						Routes: caddyRoutes,
					},
				},
			},
		},
	}, nil
}

// GenerateJSON builds and JSON-encodes the Caddy configuration.
func (g *Generator) GenerateJSON(routes []platform.DesiredRoute) ([]byte, error) {
	cfg, err := g.Generate(routes)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, aerrors.Newf(aerrors.CodeCaddyConfigGenerationFailed, "marshal caddy config: %s", err)
	}
	return data, nil
}

// EmergencyConfig returns a minimal static 503 configuration used when the
// desired state cannot be fetched and no last-good config is available.
func (g *Generator) EmergencyConfig() *CaddyConfig {
	return &CaddyConfig{
		Admin: &AdminConfig{Listen: adminListenAddress},
		Apps: &AppsConfig{
			HTTP: &HTTPApp{
				Servers: map[string]*HTTPServer{
					"srv0": {
						Listen: []string{":80"},
						Routes: []*Route{{
							Handle: []Handler{{
								Handler:    "static_response",
								StatusCode: 503,
								Body:       "Caddy Gateway em modo de emergencia.",
							}},
						}},
					},
				},
			},
		},
	}
}

// EmergencyConfigJSON returns the emergency config as JSON bytes.
func (g *Generator) EmergencyConfigJSON() ([]byte, error) {
	data, err := json.MarshalIndent(g.EmergencyConfig(), "", "  ")
	if err != nil {
		return nil, aerrors.Newf(aerrors.CodeCaddyConfigGenerationFailed, "marshal emergency config: %s", err)
	}
	return data, nil
}

// ValidateHost checks that a host is a valid FQDN-like hostname.
// Rejects empty strings, IP addresses, localhost, and hosts with invalid characters.
func ValidateHost(host string) error {
	if host == "" {
		return aerrors.New(aerrors.CodeInvalidHost, "host must not be empty")
	}
	h := strings.ToLower(strings.TrimSpace(host))

	blocked := map[string]bool{
		"localhost": true,
		"127.0.0.1": true,
		"0.0.0.0":   true,
		"::1":       true,
	}
	if blocked[h] {
		return aerrors.Newf(aerrors.CodeInvalidHost, "host %q is not allowed", host)
	}

	// Reject raw IP addresses — hosts must be domain names.
	if net.ParseIP(h) != nil {
		return aerrors.Newf(aerrors.CodeInvalidHost, "host %q must be a domain name, not an IP address", host)
	}

	if !hostPattern.MatchString(h) {
		return aerrors.Newf(aerrors.CodeInvalidHost, "host %q contains invalid characters", host)
	}
	if !strings.Contains(h, ".") {
		return aerrors.Newf(aerrors.CodeInvalidHost, "host %q must be a fully qualified domain name", host)
	}
	return nil
}

// ValidateUpstream checks that an upstream is a safe private IP:port address.
// Blocks the AWS metadata service, loopback, wildcard, and non-IP hostnames.
func ValidateUpstream(upstream string) error {
	if upstream == "" {
		return aerrors.New(aerrors.CodeInvalidUpstream, "upstream must not be empty")
	}

	host, port, err := net.SplitHostPort(upstream)
	if err != nil {
		return aerrors.Newf(aerrors.CodeInvalidUpstream, "upstream %q is not valid host:port: %s", upstream, err)
	}
	if port == "" || port == "0" {
		return aerrors.Newf(aerrors.CodeInvalidUpstream, "upstream %q must include a non-zero port", upstream)
	}

	if blockedUpstreamHosts[strings.ToLower(host)] {
		return aerrors.Newf(aerrors.CodeInvalidUpstream, "upstream host %q is not allowed", host)
	}

	ip := net.ParseIP(host)
	if ip == nil {
		// DNS names are not permitted in the MVP — upstreams must be private IP:port.
		return aerrors.Newf(aerrors.CodeInvalidUpstream, "upstream %q must use an IP address, not a hostname", upstream)
	}
	if ip.IsLoopback() {
		return aerrors.Newf(aerrors.CodeInvalidUpstream, "upstream %q uses a loopback address", upstream)
	}
	if ip.IsUnspecified() {
		return aerrors.Newf(aerrors.CodeInvalidUpstream, "upstream %q uses an unspecified/wildcard address", upstream)
	}
	return nil
}
