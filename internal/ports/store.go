package ports

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"os"

	aerrors "devex-agent/internal/errors"
)

const currentSchemaVersion = 1

// loadPortState reads ports.json from path.
// Returns an empty PortState (with no allocations) when the file does not yet exist.
func loadPortState(path string) (*PortState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if stderrors.Is(err, os.ErrNotExist) {
			return &PortState{
				SchemaVersion: currentSchemaVersion,
				Allocations:   make(map[string]*PortAllocation),
			}, nil
		}
		return nil, aerrors.Newf(aerrors.CodeStateLoadFailed, "cannot read %q: %s", path, err)
	}

	if err := checkSchemaVersion(data, path); err != nil {
		return nil, err
	}

	var state PortState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, aerrors.Newf(aerrors.CodeStateCorrupted, "cannot parse %q: %s", path, err)
	}
	if state.Allocations == nil {
		state.Allocations = make(map[string]*PortAllocation)
	}
	return &state, nil
}

// savePortState writes state to path atomically (write-to-temp → rename).
func savePortState(path string, state *PortState) error {
	state.SchemaVersion = currentSchemaVersion

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return aerrors.Newf(aerrors.CodeStateWriteFailed, "cannot marshal port state: %s", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return aerrors.Newf(aerrors.CodeStateWriteFailed, "cannot write %q: %s", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return aerrors.Newf(aerrors.CodeStateWriteFailed, "cannot rename %q to %q: %s", tmp, path, err)
	}
	return nil
}

// schemaEnvelope is used to extract schema_version before full deserialization.
type schemaEnvelope struct {
	SchemaVersion int `json:"schema_version"`
}

func checkSchemaVersion(data []byte, path string) error {
	var env schemaEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return aerrors.Newf(aerrors.CodeStateCorrupted,
			"cannot parse schema_version in %q: %s", path, err)
	}
	// schema_version == 0 means the field was absent; treat as compatible.
	if env.SchemaVersion != 0 && env.SchemaVersion != currentSchemaVersion {
		return aerrors.Newf(aerrors.CodeStateSchemaUnsupported,
			"unsupported schema_version %d in %q (expected %d)",
			env.SchemaVersion, path, currentSchemaVersion)
	}
	return nil
}

// portKey converts an integer port number to the string key used in Allocations.
func portKey(port int) string { return fmt.Sprintf("%d", port) }
