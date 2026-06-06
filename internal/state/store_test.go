package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	aerrors "devex-agent/internal/errors"
)

// --- helpers ---

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func sampleIdentity() *AgentIdentity {
	now := time.Now().UTC().Truncate(time.Second)
	return &AgentIdentity{
		AgentID:      "agent-dev-api-001",
		Mode:         "runtime",
		Environment:  "dev",
		Role:         "api",
		InstanceID:   "i-abc123",
		PrivateIP:    "10.0.2.25",
		RegisteredAt: now,
		LastSeenAt:   now,
	}
}

func sampleState() *LocalState {
	now := time.Now().UTC().Truncate(time.Second)
	return &LocalState{
		AgentID:                 "agent-dev-api-001",
		Mode:                    "runtime",
		Environment:             "dev",
		Role:                    "api",
		LastAppliedCommandID:    "cmd_123",
		LastSuccessfulCommandID: "cmd_123",
		Deployments: []DeploymentEntry{
			{
				DeploymentID:          "dep_456",
				Application:           "billing-api",
				Environment:           "dev",
				Image:                 "ghcr.io/useclarus/billing-api:v42",
				ContainerName:         "billing-api-dev-v42",
				HostPort:              4102,
				ContainerInternalPort: 3000,
				Status:                DeploymentStatusActive,
				RequiresRoute:         true,
				CreatedAt:             now,
				UpdatedAt:             now,
			},
		},
	}
}

// --- New ---

func TestNew_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subdir", "devex-agent")
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.Dir() != dir {
		t.Errorf("Dir(): got %q, want %q", s.Dir(), dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestNew_DirectoryAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(dir); err != nil {
		t.Fatalf("New on existing dir: %v", err)
	}
}

// --- AgentIdentity ---

func TestSaveAndLoadAgentIdentity(t *testing.T) {
	s := newStore(t)
	original := sampleIdentity()

	if err := s.SaveAgentIdentity(original); err != nil {
		t.Fatalf("SaveAgentIdentity: %v", err)
	}

	loaded, err := s.LoadAgentIdentity()
	if err != nil {
		t.Fatalf("LoadAgentIdentity: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil identity")
	}

	if loaded.AgentID != original.AgentID {
		t.Errorf("AgentID: got %q, want %q", loaded.AgentID, original.AgentID)
	}
	if loaded.PrivateIP != original.PrivateIP {
		t.Errorf("PrivateIP: got %q", loaded.PrivateIP)
	}
	if loaded.SchemaVersion != currentSchemaVersion {
		t.Errorf("SchemaVersion: got %d, want %d", loaded.SchemaVersion, currentSchemaVersion)
	}
}

func TestLoadAgentIdentity_FileNotExist(t *testing.T) {
	s := newStore(t)

	id, err := s.LoadAgentIdentity()
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if id != nil {
		t.Error("expected nil identity for missing file")
	}
}

func TestLoadAgentIdentity_CorruptedJSON(t *testing.T) {
	s := newStore(t)
	if err := os.WriteFile(s.agentPath(), []byte("{bad json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := s.LoadAgentIdentity()
	assertErrorCode(t, err, aerrors.CodeStateCorrupted)
}

func TestLoadAgentIdentity_UnsupportedSchemaVersion(t *testing.T) {
	s := newStore(t)
	content := `{"schema_version": 99, "agent_id": "agent-x"}`
	if err := os.WriteFile(s.agentPath(), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := s.LoadAgentIdentity()
	assertErrorCode(t, err, aerrors.CodeStateSchemaUnsupported)
}

func TestLoadAgentIdentity_ZeroSchemaVersionIsCompatible(t *testing.T) {
	s := newStore(t)
	// schema_version absent (zero value) → compatible with current version
	content := `{"agent_id": "agent-legacy"}`
	if err := os.WriteFile(s.agentPath(), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	id, err := s.LoadAgentIdentity()
	if err != nil {
		t.Fatalf("expected nil error for zero schema version, got: %v", err)
	}
	if id == nil || id.AgentID != "agent-legacy" {
		t.Error("expected valid identity for zero schema version")
	}
}

// --- LocalState ---

func TestSaveAndLoadLocalState(t *testing.T) {
	s := newStore(t)
	original := sampleState()

	if err := s.SaveLocalState(original); err != nil {
		t.Fatalf("SaveLocalState: %v", err)
	}

	loaded, err := s.LoadLocalState()
	if err != nil {
		t.Fatalf("LoadLocalState: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil state")
	}

	if loaded.AgentID != original.AgentID {
		t.Errorf("AgentID: got %q", loaded.AgentID)
	}
	if loaded.LastAppliedCommandID != original.LastAppliedCommandID {
		t.Errorf("LastAppliedCommandID: got %q", loaded.LastAppliedCommandID)
	}
	if len(loaded.Deployments) != 1 {
		t.Fatalf("Deployments: got %d, want 1", len(loaded.Deployments))
	}
	d := loaded.Deployments[0]
	if d.DeploymentID != "dep_456" {
		t.Errorf("DeploymentID: got %q", d.DeploymentID)
	}
	if d.Status != DeploymentStatusActive {
		t.Errorf("Status: got %q", d.Status)
	}
	if d.HostPort != 4102 {
		t.Errorf("HostPort: got %d", d.HostPort)
	}
	if loaded.SchemaVersion != currentSchemaVersion {
		t.Errorf("SchemaVersion: got %d, want %d", loaded.SchemaVersion, currentSchemaVersion)
	}
}

func TestLoadLocalState_FileNotExist(t *testing.T) {
	s := newStore(t)

	state, err := s.LoadLocalState()
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if state != nil {
		t.Error("expected nil state for missing file")
	}
}

func TestLoadLocalState_CorruptedJSON(t *testing.T) {
	s := newStore(t)
	if err := os.WriteFile(s.statePath(), []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := s.LoadLocalState()
	assertErrorCode(t, err, aerrors.CodeStateCorrupted)
}

func TestLoadLocalState_UnsupportedSchemaVersion(t *testing.T) {
	s := newStore(t)
	content := `{"schema_version": 42, "agent_id": "x"}`
	if err := os.WriteFile(s.statePath(), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := s.LoadLocalState()
	assertErrorCode(t, err, aerrors.CodeStateSchemaUnsupported)
}

// --- Atomic write ---

func TestAtomicWrite_NoTempFileAfterSuccess(t *testing.T) {
	s := newStore(t)
	id := sampleIdentity()

	if err := s.SaveAgentIdentity(id); err != nil {
		t.Fatalf("SaveAgentIdentity: %v", err)
	}

	tmpPath := s.agentPath() + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temporary file should not exist after successful write")
	}
}

func TestAtomicWrite_OverwritePreservesData(t *testing.T) {
	s := newStore(t)

	first := sampleIdentity()
	first.AgentID = "first-agent"
	if err := s.SaveAgentIdentity(first); err != nil {
		t.Fatalf("first save: %v", err)
	}

	second := sampleIdentity()
	second.AgentID = "second-agent"
	if err := s.SaveAgentIdentity(second); err != nil {
		t.Fatalf("second save: %v", err)
	}

	loaded, err := s.LoadAgentIdentity()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.AgentID != "second-agent" {
		t.Errorf("AgentID: got %q, want %q", loaded.AgentID, "second-agent")
	}
}

// --- Deployment status constants ---

func TestDeploymentStatusValues(t *testing.T) {
	statuses := []DeploymentStatus{
		DeploymentStatusReserved,
		DeploymentStatusStarting,
		DeploymentStatusCheckingHealth,
		DeploymentStatusActive,
		DeploymentStatusDraining,
		DeploymentStatusFailed,
		DeploymentStatusRemoved,
		DeploymentStatusOrphaned,
		DeploymentStatusInconsistent,
	}
	for _, s := range statuses {
		if s == "" {
			t.Error("deployment status constant must not be empty")
		}
	}
}

// --- Gateway config ---

func TestSaveAndLoadGatewayConfig(t *testing.T) {
	s := newStore(t)
	if err := s.EnsureGatewayDir(); err != nil {
		t.Fatalf("EnsureGatewayDir: %v", err)
	}

	content := []byte(`{"admin":{"listen":"0.0.0.0:2019"}}`)
	if err := s.SaveGatewayConfig("current-caddy.json", content); err != nil {
		t.Fatalf("SaveGatewayConfig: %v", err)
	}

	loaded, err := s.LoadGatewayConfig("current-caddy.json")
	if err != nil {
		t.Fatalf("LoadGatewayConfig: %v", err)
	}
	if string(loaded) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", loaded, content)
	}
}

func TestLoadGatewayConfig_FileNotExist(t *testing.T) {
	s := newStore(t)
	if err := s.EnsureGatewayDir(); err != nil {
		t.Fatal(err)
	}

	data, err := s.LoadGatewayConfig("missing.json")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if data != nil {
		t.Error("expected nil data for missing file")
	}
}

// --- helpers ---

func assertErrorCode(t *testing.T, err error, code aerrors.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %q, got nil", code)
	}
	got := aerrors.CodeOf(err)
	if got != code {
		t.Errorf("error code: got %q, want %q (error: %v)", got, code, err)
	}
}
