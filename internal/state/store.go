package state

import (
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"

	aerrors "devex-agent/internal/errors"
)

const currentSchemaVersion = 1

// Store manages local agent state files under a base directory.
// Files are written atomically (write-to-temp, rename).
type Store struct {
	dir string
}

// New creates a Store and ensures the base state directory exists with
// restricted permissions (0700).
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, aerrors.Newf(aerrors.CodeStateStoreFailed,
			"cannot create state directory %q: %s", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Dir returns the base state directory path.
func (s *Store) Dir() string { return s.dir }

// LoadAgentIdentity loads agent.json.
// Returns (nil, nil) when the file does not yet exist (first boot).
func (s *Store) LoadAgentIdentity() (*AgentIdentity, error) {
	return loadJSON[AgentIdentity](s.agentPath())
}

// SaveAgentIdentity writes agent.json atomically.
func (s *Store) SaveAgentIdentity(id *AgentIdentity) error {
	id.SchemaVersion = currentSchemaVersion
	return saveJSON(s.agentPath(), id)
}

// LoadLocalState loads state.json.
// Returns (nil, nil) when the file does not yet exist (first boot).
func (s *Store) LoadLocalState() (*LocalState, error) {
	return loadJSON[LocalState](s.statePath())
}

// SaveLocalState writes state.json atomically.
func (s *Store) SaveLocalState(state *LocalState) error {
	state.SchemaVersion = currentSchemaVersion
	return saveJSON(s.statePath(), state)
}

func (s *Store) agentPath() string { return filepath.Join(s.dir, "agent.json") }
func (s *Store) statePath() string { return filepath.Join(s.dir, "state.json") }

// loadJSON reads and deserializes a JSON file into T.
// Returns (nil, nil) if the file does not exist.
// Returns a typed error if the file is corrupted or has an unsupported schema.
func loadJSON[T any](path string) (*T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if stderrors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, aerrors.Newf(aerrors.CodeStateLoadFailed,
			"cannot read %q: %s", path, err)
	}

	if err := checkSchemaVersion(data, path); err != nil {
		return nil, err
	}

	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, aerrors.Newf(aerrors.CodeStateCorrupted,
			"cannot parse %q: %s", path, err)
	}

	return &v, nil
}

// saveJSON serializes v to JSON and writes to path atomically.
func saveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return aerrors.Newf(aerrors.CodeStateWriteFailed,
			"cannot marshal state for %q: %s", path, err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return aerrors.Newf(aerrors.CodeStateWriteFailed,
			"cannot write temporary file %q: %s", tmp, err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return aerrors.Newf(aerrors.CodeStateWriteFailed,
			"cannot rename %q to %q: %s", tmp, path, err)
	}

	return nil
}

// schemaVersionEnvelope is used only to extract schema_version before full
// deserialization, so we can detect unsupported schema versions early.
type schemaVersionEnvelope struct {
	SchemaVersion int `json:"schema_version"`
}

func checkSchemaVersion(data []byte, path string) error {
	var env schemaVersionEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return aerrors.Newf(aerrors.CodeStateCorrupted,
			"cannot parse schema_version in %q: %s", path, err)
	}
	// schema_version == 0 means the field is absent; treat as compatible.
	if env.SchemaVersion != 0 && env.SchemaVersion != currentSchemaVersion {
		return aerrors.Newf(aerrors.CodeStateSchemaUnsupported,
			"unsupported schema_version %d in %q (expected %d)",
			env.SchemaVersion, path, currentSchemaVersion)
	}
	return nil
}

// EnsureGatewayDir creates the gateway subdirectory for Caddy config files.
func (s *Store) EnsureGatewayDir() error {
	dir := filepath.Join(s.dir, "gateway")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return aerrors.Newf(aerrors.CodeStateStoreFailed,
			"cannot create gateway directory %q: %s", dir, err)
	}
	return nil
}

// EnsureLocksDir creates the locks subdirectory.
func (s *Store) EnsureLocksDir() error {
	dir := filepath.Join(s.dir, "locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return aerrors.Newf(aerrors.CodeStateStoreFailed,
			"cannot create locks directory %q: %s", dir, err)
	}
	return nil
}

// GatewayFilePath returns the absolute path for a gateway config file name.
func (s *Store) GatewayFilePath(name string) string {
	return filepath.Join(s.dir, "gateway", name)
}

// SaveGatewayConfig writes a Caddy JSON config atomically to the gateway directory.
func (s *Store) SaveGatewayConfig(name string, content []byte) error {
	path := s.GatewayFilePath(name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0600); err != nil {
		return aerrors.Newf(aerrors.CodeStateWriteFailed,
			"cannot write %q: %s", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return aerrors.Newf(aerrors.CodeStateWriteFailed,
			"cannot rename %q to %q: %s", tmp, path, err)
	}
	return nil
}

// LoadGatewayConfig reads a Caddy JSON config from the gateway directory.
// Returns (nil, nil) if the file does not exist.
func (s *Store) LoadGatewayConfig(name string) ([]byte, error) {
	path := s.GatewayFilePath(name)
	data, err := os.ReadFile(path)
	if err != nil {
		if stderrors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, aerrors.Newf(aerrors.CodeStateLoadFailed,
			"cannot read %q: %s", path, err)
	}
	return data, nil
}

