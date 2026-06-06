package errors

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"testing"
)

func TestNew_NonRetryable(t *testing.T) {
	err := New(CodeImagePullFailed, "could not pull image")

	if err.Code != CodeImagePullFailed {
		t.Errorf("code: got %q, want %q", err.Code, CodeImagePullFailed)
	}
	if err.Message != "could not pull image" {
		t.Errorf("message: got %q", err.Message)
	}
	if err.Retryable {
		t.Error("expected Retryable=false")
	}
	if err.Operation != "" {
		t.Errorf("operation: expected empty, got %q", err.Operation)
	}
}

func TestNewf_FormatsMessage(t *testing.T) {
	err := Newf(CodePortAllocationFailed, "no port available in range %d-%d", 4100, 4114)

	if err.Message != "no port available in range 4100-4114" {
		t.Errorf("message: got %q", err.Message)
	}
	if err.Code != CodePortAllocationFailed {
		t.Errorf("code: got %q", err.Code)
	}
}

func TestNewRetryable(t *testing.T) {
	err := NewRetryable(CodePlatformAPIUnavailable, "connection refused")

	if !err.Retryable {
		t.Error("expected Retryable=true")
	}
	if err.Code != CodePlatformAPIUnavailable {
		t.Errorf("code: got %q", err.Code)
	}
}

func TestWrap_PreservesCause(t *testing.T) {
	cause := stderrors.New("dial tcp: connection refused")
	err := Wrap(CodePlatformAPIUnavailable, "platform.heartbeat", cause)

	if err.Code != CodePlatformAPIUnavailable {
		t.Errorf("code: got %q, want %q", err.Code, CodePlatformAPIUnavailable)
	}
	if err.Operation != "platform.heartbeat" {
		t.Errorf("operation: got %q", err.Operation)
	}
	if err.Retryable {
		t.Error("expected Retryable=false")
	}
	if !stderrors.Is(err, cause) {
		t.Error("expected cause to be reachable via errors.Is")
	}
}

func TestWrapRetryable(t *testing.T) {
	cause := fmt.Errorf("timeout")
	err := WrapRetryable(CodePlatformAPIUnavailable, "platform.fetch", cause)

	if !err.Retryable {
		t.Error("expected Retryable=true")
	}
	if err.Code != CodePlatformAPIUnavailable {
		t.Errorf("code: got %q", err.Code)
	}
}

func TestWrap_CodePreservedThroughChain(t *testing.T) {
	cause := New(CodeStateCorrupted, "bad json")
	wrapped := Wrap(CodeStateLoadFailed, "state.load", cause)

	// outer code
	if CodeOf(wrapped) != CodeStateLoadFailed {
		t.Errorf("outer code: got %q", CodeOf(wrapped))
	}
	// cause is reachable
	var inner *OperationalError
	if !stderrors.As(wrapped, &inner) {
		t.Fatal("expected inner OperationalError via errors.As")
	}
}

func TestError_FormatWithOperation(t *testing.T) {
	err := &OperationalError{
		Code:      CodeHealthCheckFailed,
		Message:   "application not ready",
		Operation: "health.http",
	}
	want := "[HEALTH_CHECK_FAILED] health.http: application not ready"
	if got := err.Error(); got != want {
		t.Errorf("Error(): got %q, want %q", got, want)
	}
}

func TestError_FormatWithoutOperation(t *testing.T) {
	err := New(CodeContainerStartFailed, "exit code 1")
	want := "[CONTAINER_START_FAILED] exit code 1"
	if got := err.Error(); got != want {
		t.Errorf("Error(): got %q, want %q", got, want)
	}
}

func TestIsRetryable_True(t *testing.T) {
	err := NewRetryable(CodePlatformAPIUnavailable, "unavailable")
	if !IsRetryable(err) {
		t.Error("expected IsRetryable=true")
	}
}

func TestIsRetryable_False(t *testing.T) {
	err := New(CodeImageNotFound, "image not found")
	if IsRetryable(err) {
		t.Error("expected IsRetryable=false")
	}
}

func TestIsRetryable_NonOperationalError(t *testing.T) {
	err := stderrors.New("plain error")
	if IsRetryable(err) {
		t.Error("expected IsRetryable=false for plain error")
	}
}

func TestCodeOf_ReturnsCode(t *testing.T) {
	err := New(CodeDockerUnavailable, "docker not found")
	if got := CodeOf(err); got != CodeDockerUnavailable {
		t.Errorf("CodeOf: got %q, want %q", got, CodeDockerUnavailable)
	}
}

func TestCodeOf_NonOperationalError(t *testing.T) {
	err := stderrors.New("plain error")
	if got := CodeOf(err); got != "" {
		t.Errorf("CodeOf: expected empty, got %q", got)
	}
}

func TestCodeOf_Nil(t *testing.T) {
	if got := CodeOf(nil); got != "" {
		t.Errorf("CodeOf(nil): expected empty, got %q", got)
	}
}

func TestJSONSerialization(t *testing.T) {
	err := &OperationalError{
		Code:      CodeHealthCheckFailed,
		Message:   "no response",
		Operation: "health.http",
		Retryable: false,
	}

	data, jsonErr := json.Marshal(err)
	if jsonErr != nil {
		t.Fatalf("json.Marshal: %v", jsonErr)
	}

	var decoded OperationalError
	if jsonErr = json.Unmarshal(data, &decoded); jsonErr != nil {
		t.Fatalf("json.Unmarshal: %v", jsonErr)
	}

	if decoded.Code != err.Code {
		t.Errorf("code: got %q, want %q", decoded.Code, err.Code)
	}
	if decoded.Message != err.Message {
		t.Errorf("message: got %q", decoded.Message)
	}
	if decoded.Operation != err.Operation {
		t.Errorf("operation: got %q", decoded.Operation)
	}
	if decoded.Retryable != err.Retryable {
		t.Errorf("retryable: got %v", decoded.Retryable)
	}
}

func TestJSONSerialization_CauseExcluded(t *testing.T) {
	cause := stderrors.New("internal cause")
	err := Wrap(CodeStateLoadFailed, "state.load", cause)

	data, jsonErr := json.Marshal(err)
	if jsonErr != nil {
		t.Fatalf("json.Marshal: %v", jsonErr)
	}

	// The Cause field (error interface) must not produce a "cause" JSON key.
	// The message field may contain the cause text — that is expected.
	var m map[string]any
	if jsonErr = json.Unmarshal(data, &m); jsonErr != nil {
		t.Fatalf("json.Unmarshal: %v", jsonErr)
	}
	if _, ok := m["cause"]; ok {
		t.Errorf("json output must not contain a 'cause' key: %s", data)
	}
}

func TestIs_Wrapper(t *testing.T) {
	sentinel := stderrors.New("sentinel")
	err := Wrap(CodeStateLoadFailed, "op", sentinel)

	if !Is(err, sentinel) {
		t.Error("Is: expected true for wrapped sentinel")
	}
}

func TestAs_Wrapper(t *testing.T) {
	original := New(CodeCaddyLoadFailed, "load failed")

	var target *OperationalError
	if !As(original, &target) {
		t.Fatal("As: expected true")
	}
	if target.Code != CodeCaddyLoadFailed {
		t.Errorf("As: code got %q", target.Code)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
