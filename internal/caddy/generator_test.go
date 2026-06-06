package caddy

import (
	"encoding/json"
	"strings"
	"testing"

	"devex-agent/internal/platform"
)

// ============================================================
// Generate / GenerateJSON
// ============================================================

func TestGenerate_TwoRoutes(t *testing.T) {
	g := NewGenerator(nil)
	routes := []platform.DesiredRoute{
		{ID: "r1", Host: "billing-api.dev.example.com", Upstream: "10.0.2.25:4102", HealthCheckPath: "/health"},
		{ID: "r2", Host: "orders-api.dev.example.com", Upstream: "10.0.2.31:4103", HealthCheckPath: "/health"},
	}

	cfg, err := g.Generate(routes)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	srv, ok := cfg.Apps.HTTP.Servers["srv0"]
	if !ok {
		t.Fatal("server srv0 missing")
	}
	if len(srv.Routes) != 2 {
		t.Fatalf("routes: got %d, want 2", len(srv.Routes))
	}

	// First route (sorted: billing < orders).
	r := srv.Routes[0]
	if len(r.Match) == 0 || len(r.Match[0].Host) == 0 {
		t.Fatal("match missing on first route")
	}
	if r.Match[0].Host[0] != "billing-api.dev.example.com" {
		t.Errorf("host: got %q", r.Match[0].Host[0])
	}
	if len(r.Handle) == 0 {
		t.Fatal("handle missing on first route")
	}
	if r.Handle[0].Handler != "reverse_proxy" {
		t.Errorf("handler: got %q", r.Handle[0].Handler)
	}
	if len(r.Handle[0].Upstreams) == 0 || r.Handle[0].Upstreams[0].Dial != "10.0.2.25:4102" {
		t.Errorf("upstream dial: got %+v", r.Handle[0].Upstreams)
	}
}

func TestGenerate_SortsByHost(t *testing.T) {
	g := NewGenerator(nil)
	routes := []platform.DesiredRoute{
		{Host: "z-service.dev.example.com", Upstream: "10.0.0.3:4103"},
		{Host: "a-service.dev.example.com", Upstream: "10.0.0.1:4101"},
		{Host: "m-service.dev.example.com", Upstream: "10.0.0.2:4102"},
	}

	cfg, err := g.Generate(routes)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	srv := cfg.Apps.HTTP.Servers["srv0"]
	hosts := make([]string, len(srv.Routes))
	for i, r := range srv.Routes {
		hosts[i] = r.Match[0].Host[0]
	}
	if hosts[0] != "a-service.dev.example.com" || hosts[1] != "m-service.dev.example.com" || hosts[2] != "z-service.dev.example.com" {
		t.Errorf("routes not sorted: %v", hosts)
	}
}

func TestGenerate_EmptyRoutes(t *testing.T) {
	g := NewGenerator(nil)
	cfg, err := g.Generate(nil)
	if err != nil {
		t.Fatalf("Generate with no routes: %v", err)
	}
	srv := cfg.Apps.HTTP.Servers["srv0"]
	if len(srv.Routes) != 0 {
		t.Errorf("expected 0 routes, got %d", len(srv.Routes))
	}
}

func TestGenerate_ContainsAdminConfig(t *testing.T) {
	g := NewGenerator(nil)
	cfg, err := g.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if cfg.Admin == nil {
		t.Fatal("admin config missing")
	}
	if cfg.Admin.Listen != "0.0.0.0:2019" {
		t.Errorf("admin listen: got %q", cfg.Admin.Listen)
	}
}

func TestGenerate_CustomListenAddresses(t *testing.T) {
	g := NewGenerator([]string{":8080"})
	cfg, err := g.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	srv := cfg.Apps.HTTP.Servers["srv0"]
	if len(srv.Listen) != 1 || srv.Listen[0] != ":8080" {
		t.Errorf("listen: got %v", srv.Listen)
	}
}

func TestGenerate_InvalidHost_ReturnsError(t *testing.T) {
	g := NewGenerator(nil)
	routes := []platform.DesiredRoute{
		{Host: "localhost", Upstream: "10.0.2.25:4102"},
	}
	_, err := g.Generate(routes)
	if err == nil {
		t.Fatal("expected error for invalid host 'localhost'")
	}
	if !strings.Contains(err.Error(), "INVALID_HOST") {
		t.Errorf("error should contain INVALID_HOST, got: %v", err)
	}
}

func TestGenerate_InvalidUpstream_ReturnsError(t *testing.T) {
	g := NewGenerator(nil)
	routes := []platform.DesiredRoute{
		{Host: "api.dev.example.com", Upstream: "169.254.169.254:80"},
	}
	_, err := g.Generate(routes)
	if err == nil {
		t.Fatal("expected error for metadata service upstream")
	}
	if !strings.Contains(err.Error(), "INVALID_UPSTREAM") {
		t.Errorf("error should contain INVALID_UPSTREAM, got: %v", err)
	}
}

func TestGenerateJSON_ProducesValidJSON(t *testing.T) {
	g := NewGenerator(nil)
	routes := []platform.DesiredRoute{
		{Host: "api.dev.example.com", Upstream: "10.0.0.1:4100"},
	}
	data, err := g.GenerateJSON(routes)
	if err != nil {
		t.Fatalf("GenerateJSON: %v", err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Errorf("output is not valid JSON: %v", err)
	}
}

func TestGenerateJSON_IsDeterministic(t *testing.T) {
	g := NewGenerator(nil)
	routes := []platform.DesiredRoute{
		{Host: "z.dev.example.com", Upstream: "10.0.0.2:4101"},
		{Host: "a.dev.example.com", Upstream: "10.0.0.1:4100"},
	}
	first, err := g.GenerateJSON(routes)
	if err != nil {
		t.Fatalf("first GenerateJSON: %v", err)
	}
	second, err := g.GenerateJSON(routes)
	if err != nil {
		t.Fatalf("second GenerateJSON: %v", err)
	}
	if string(first) != string(second) {
		t.Error("GenerateJSON is not deterministic")
	}
}

// ============================================================
// EmergencyConfig
// ============================================================

func TestEmergencyConfig_Is503(t *testing.T) {
	g := NewGenerator(nil)
	cfg := g.EmergencyConfig()

	srv, ok := cfg.Apps.HTTP.Servers["srv0"]
	if !ok {
		t.Fatal("srv0 missing from emergency config")
	}
	if len(srv.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(srv.Routes))
	}
	h := srv.Routes[0].Handle
	if len(h) == 0 {
		t.Fatal("no handler in emergency route")
	}
	if h[0].Handler != "static_response" {
		t.Errorf("handler: got %q, want static_response", h[0].Handler)
	}
	if h[0].StatusCode != 503 {
		t.Errorf("status_code: got %d, want 503", h[0].StatusCode)
	}
	if h[0].Body == "" {
		t.Error("emergency body must not be empty")
	}
}

func TestEmergencyConfig_HasNoMatch(t *testing.T) {
	g := NewGenerator(nil)
	cfg := g.EmergencyConfig()
	srv := cfg.Apps.HTTP.Servers["srv0"]
	// Emergency route must have no match (catches all).
	if len(srv.Routes[0].Match) != 0 {
		t.Error("emergency route must not have match conditions")
	}
}

func TestEmergencyConfigJSON_ValidJSON(t *testing.T) {
	g := NewGenerator(nil)
	data, err := g.EmergencyConfigJSON()
	if err != nil {
		t.Fatalf("EmergencyConfigJSON: %v", err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Errorf("emergency config is not valid JSON: %v", err)
	}
}

// ============================================================
// ValidateHost
// ============================================================

func TestValidateHost_Valid(t *testing.T) {
	cases := []string{
		"billing-api.dev.useclarus.app",
		"orders-api.stage.useclarus.app",
		"api.useclarus.app",
		"my-service.internal.example.com",
	}
	for _, h := range cases {
		t.Run(h, func(t *testing.T) {
			if err := ValidateHost(h); err != nil {
				t.Errorf("ValidateHost(%q): unexpected error: %v", h, err)
			}
		})
	}
}

func TestValidateHost_Invalid(t *testing.T) {
	cases := []struct {
		host string
		why  string
	}{
		{"", "empty"},
		{"localhost", "blocked name"},
		{"127.0.0.1", "IP address"},
		{"0.0.0.0", "wildcard IP"},
		{"::1", "IPv6 loopback"},
		{"10.0.2.25", "private IP"},
		{"my service", "space in host"},
		{"nodot", "no dot (not FQDN)"},
		{"a.b.c.d.e.f.g.h$", "invalid character"},
	}
	for _, tc := range cases {
		t.Run(tc.why, func(t *testing.T) {
			err := ValidateHost(tc.host)
			if err == nil {
				t.Errorf("ValidateHost(%q): expected error (%s), got nil", tc.host, tc.why)
			}
		})
	}
}

// ============================================================
// ValidateUpstream
// ============================================================

func TestValidateUpstream_Valid(t *testing.T) {
	cases := []string{
		"10.0.2.25:4102",
		"10.0.3.31:4103",
		"192.168.1.100:8080",
		"172.16.0.5:9000",
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			if err := ValidateUpstream(u); err != nil {
				t.Errorf("ValidateUpstream(%q): unexpected error: %v", u, err)
			}
		})
	}
}

func TestValidateUpstream_Invalid(t *testing.T) {
	cases := []struct {
		upstream string
		why      string
	}{
		{"", "empty"},
		{"169.254.169.254:80", "metadata service"},
		{"127.0.0.1:22", "loopback IP"},
		{"localhost:8080", "localhost name"},
		{"0.0.0.0:80", "wildcard IP"},
		{"10.0.2.25", "missing port"},
		{"10.0.2.25:0", "port zero"},
		{"billing-api:8080", "hostname not IP"},
		{"::1:8080", "IPv6 loopback"},
	}
	for _, tc := range cases {
		t.Run(tc.why, func(t *testing.T) {
			err := ValidateUpstream(tc.upstream)
			if err == nil {
				t.Errorf("ValidateUpstream(%q): expected error (%s), got nil", tc.upstream, tc.why)
			}
		})
	}
}
