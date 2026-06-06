package docker

import "time"

// ContainerSpec describes a container to be created by the Runtime Agent.
type ContainerSpec struct {
	Name          string
	Image         string
	HostPort      int               // allocated by the Port Manager; 0 for workers
	ContainerPort int               // port the application listens on inside the container
	Env           map[string]string // values must never be logged
	Labels        map[string]string
	RestartPolicy string
	Network       string
}

// ContainerInfo holds the runtime state of a container.
type ContainerInfo struct {
	ID            string
	Name          string
	Image         string
	Status        string
	Running       bool
	ExitCode      int
	HostPort      int
	ContainerPort int
	Labels        map[string]string
	CreatedAt     time.Time
}

// ContainerFilter defines criteria for listing containers.
type ContainerFilter struct {
	ManagedOnly bool              // restrict to containers labelled devex.managed=true
	Labels      map[string]string // additional required labels
	NamePrefix  string            // optional name prefix applied client-side
}

// CommandResult holds the captured output of a CLI command.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ExecCall records one command invocation; used by FakeExecutor for assertions.
type ExecCall struct {
	Name string
	Args []string
}
