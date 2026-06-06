package docker

import (
	"bytes"
	"context"
	"os/exec"
	"sync"
	"time"

	aerrors "devex-agent/internal/errors"
)

// Runtime is the Docker operations interface.
// All Runtime Agent code depends on this interface, not on concrete implementations,
// which allows the MVP CLI implementation to be replaced with the Docker SDK later.
type Runtime interface {
	PullImage(ctx context.Context, image string) error
	StartContainer(ctx context.Context, spec ContainerSpec) (*ContainerInfo, error)
	StopContainer(ctx context.Context, name string, timeout time.Duration) error
	RemoveContainer(ctx context.Context, name string) error
	InspectContainer(ctx context.Context, name string) (*ContainerInfo, error)
	ListContainers(ctx context.Context, filter ContainerFilter) ([]ContainerInfo, error)
}

// CommandExecutor runs a system command and returns its output.
// Abstracting this allows FakeExecutor to be used in tests without a real Docker daemon.
type CommandExecutor interface {
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}

// --- OSExecutor ---

// OSExecutor runs real OS commands via os/exec.
type OSExecutor struct{}

// Run executes the command, captures stdout+stderr, and returns the result.
// A non-zero exit code is NOT returned as a Go error; the caller must inspect
// result.ExitCode. A Go error is returned only when the process cannot start
// (binary not found, permission denied) or the context is cancelled/timed out.
func (e *OSExecutor) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Non-zero exit: Docker command ran but failed. Caller checks ExitCode.
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		// Process couldn't start, or context was cancelled/timed out.
		return result, aerrors.WrapRetryable(aerrors.CodeDockerUnavailable, "exec.Run", err)
	}
	return result, nil
}

// --- FakeExecutor ---

// FakeExecutor records command calls and returns pre-queued responses in order.
// It is exported so tests in other packages (e.g., internal/agent) can use it.
//
// Usage:
//
//	fake := &docker.FakeExecutor{}
//	fake.Enqueue(docker.CommandResult{Stdout: "abc123\n"})
//	runtime := docker.NewCLIRuntime("docker", fake, logger)
type FakeExecutor struct {
	mu        sync.Mutex
	responses []CommandResult
	Calls     []ExecCall
}

// Enqueue adds one or more responses to be consumed in order by Run.
func (f *FakeExecutor) Enqueue(results ...CommandResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses = append(f.responses, results...)
}

// Run records the invocation and returns the next queued response.
// When the queue is empty, it returns an empty success result.
func (f *FakeExecutor) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, ExecCall{Name: name, Args: args})
	if len(f.responses) == 0 {
		return CommandResult{ExitCode: 0}, nil
	}
	r := f.responses[0]
	f.responses = f.responses[1:]
	return r, nil
}

// CallCount returns the total number of commands recorded.
func (f *FakeExecutor) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Calls)
}

// LastCall returns the most recent recorded call.
func (f *FakeExecutor) LastCall() ExecCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.Calls) == 0 {
		return ExecCall{}
	}
	return f.Calls[len(f.Calls)-1]
}

// CallAt returns the call at a given index (0-based).
func (f *FakeExecutor) CallAt(i int) ExecCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 || i >= len(f.Calls) {
		return ExecCall{}
	}
	return f.Calls[i]
}

// Reset clears all recorded calls and queued responses.
func (f *FakeExecutor) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses = nil
	f.Calls = nil
}
