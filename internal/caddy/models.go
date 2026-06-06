package caddy

// CaddyConfig is the root configuration object submitted to the Caddy Admin API via /load.
type CaddyConfig struct {
	Admin *AdminConfig `json:"admin,omitempty"`
	Apps  *AppsConfig  `json:"apps,omitempty"`
}

// AdminConfig configures the Caddy Admin API listener.
type AdminConfig struct {
	Listen string `json:"listen"`
}

// AppsConfig is the top-level apps section of the Caddy config.
type AppsConfig struct {
	HTTP *HTTPApp `json:"http,omitempty"`
}

// HTTPApp configures Caddy HTTP servers.
type HTTPApp struct {
	Servers map[string]*HTTPServer `json:"servers"`
}

// HTTPServer is a single Caddy HTTP server entry.
type HTTPServer struct {
	Listen []string `json:"listen"`
	Routes []*Route `json:"routes"`
}

// Route is a Caddy routing rule: zero or more match conditions, one or more handlers.
type Route struct {
	Match  []MatchConfig `json:"match,omitempty"`
	Handle []Handler     `json:"handle"`
}

// MatchConfig selects requests based on hostname.
type MatchConfig struct {
	Host []string `json:"host,omitempty"`
}

// Handler describes a single Caddy request handler.
// The Handler field is the discriminator ("reverse_proxy", "static_response", etc.).
type Handler struct {
	Handler string `json:"handler"`

	// reverse_proxy fields
	Upstreams []Upstream `json:"upstreams,omitempty"`

	// static_response fields
	StatusCode int    `json:"status_code,omitempty"`
	Body       string `json:"body,omitempty"`
}

// Upstream is a backend target for the reverse_proxy handler.
type Upstream struct {
	Dial string `json:"dial"`
}
