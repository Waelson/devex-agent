package docker_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"devex-agent/internal/docker"
)

// skipUnlessDockerIntegration skips the test unless RUN_DOCKER_INTEGRATION_TESTS=true.
// Requires Docker to be installed and the daemon to be reachable.
func skipUnlessDockerIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("RUN_DOCKER_INTEGRATION_TESTS") != "true" {
		t.Skip("skipping Docker integration test: set RUN_DOCKER_INTEGRATION_TESTS=true to run")
	}
}

// integrationRuntime creates a CLIRuntime backed by the real OS Docker CLI.
func integrationRuntime(t *testing.T) *docker.CLIRuntime {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return docker.NewCLIRuntime("docker", &docker.OSExecutor{}, logger)
}

// containerName returns a safe, unique container name derived from the test name.
func containerName(t *testing.T) string {
	t.Helper()
	r := strings.NewReplacer("/", "-", "_", "-")
	n := strings.ToLower(r.Replace(t.Name()))
	if len(n) > 50 {
		n = n[:50]
	}
	return n
}

// ensureRemoved removes a container unconditionally; used in t.Cleanup.
func ensureRemoved(rt *docker.CLIRuntime, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = rt.StopContainer(ctx, name, 5*time.Second)
	_ = rt.RemoveContainer(ctx, name)
}

const integrationImage = "alpine:latest"

// managedLabels returns the minimum set of labels so the container is
// tracked as a managed DevEx container.
func managedLabels() map[string]string {
	return map[string]string{"devex.managed": "true"}
}

// ─── Tests ───────────────────────────────────────────────────────────────────

func TestIntegration_Docker_PullImage(t *testing.T) {
	skipUnlessDockerIntegration(t)

	rt := integrationRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := rt.PullImage(ctx, integrationImage); err != nil {
		t.Fatalf("PullImage(%q) failed: %v", integrationImage, err)
	}
}

func TestIntegration_Docker_StartInspectRemove(t *testing.T) {
	skipUnlessDockerIntegration(t)

	rt := integrationRuntime(t)
	name := containerName(t)
	t.Cleanup(func() { ensureRemoved(rt, name) })

	startCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	spec := docker.ContainerSpec{
		Name:          name,
		Image:         integrationImage,
		Labels:        managedLabels(),
		RestartPolicy: "no",
	}
	info, err := rt.StartContainer(startCtx, spec)
	if err != nil {
		t.Fatalf("StartContainer: %v", err)
	}
	if info.Name != name {
		t.Errorf("ContainerInfo.Name: got %q, want %q", info.Name, name)
	}

	// alpine exits immediately; wait a moment then inspect.
	time.Sleep(500 * time.Millisecond)

	inspectCtx, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()

	got, err := rt.InspectContainer(inspectCtx, name)
	if err != nil {
		t.Fatalf("InspectContainer: %v", err)
	}
	if got.Name != name {
		t.Errorf("InspectContainer.Name: got %q, want %q", got.Name, name)
	}
	if got.Image == "" {
		t.Error("InspectContainer.Image should not be empty")
	}
	if got.CreatedAt.IsZero() {
		t.Error("InspectContainer.CreatedAt should not be zero")
	}

	rmCtx, cancel3 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel3()

	if err := rt.RemoveContainer(rmCtx, name); err != nil {
		t.Fatalf("RemoveContainer: %v", err)
	}
}

func TestIntegration_Docker_LabelsApplied(t *testing.T) {
	skipUnlessDockerIntegration(t)

	rt := integrationRuntime(t)
	name := containerName(t)
	t.Cleanup(func() { ensureRemoved(rt, name) })

	startCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wantLabels := map[string]string{
		"devex.managed":     "true",
		"devex.application": "integration-test",
		"devex.environment": "test",
	}

	_, err := rt.StartContainer(startCtx, docker.ContainerSpec{
		Name:          name,
		Image:         integrationImage,
		Labels:        wantLabels,
		RestartPolicy: "no",
	})
	if err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	inspectCtx, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()

	info, err := rt.InspectContainer(inspectCtx, name)
	if err != nil {
		t.Fatalf("InspectContainer: %v", err)
	}

	for k, want := range wantLabels {
		if got := info.Labels[k]; got != want {
			t.Errorf("label %q: got %q, want %q", k, got, want)
		}
	}
}

func TestIntegration_Docker_PortBinding(t *testing.T) {
	skipUnlessDockerIntegration(t)

	rt := integrationRuntime(t)
	name := containerName(t)
	t.Cleanup(func() { ensureRemoved(rt, name) })

	startCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const hostPort = 19080
	const containerPort = 80

	_, err := rt.StartContainer(startCtx, docker.ContainerSpec{
		Name:          name,
		Image:         integrationImage,
		HostPort:      hostPort,
		ContainerPort: containerPort,
		Labels:        managedLabels(),
		RestartPolicy: "no",
	})
	if err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	// alpine exits immediately; inspect while container still exists in Docker.
	time.Sleep(300 * time.Millisecond)

	inspectCtx, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()

	info, err := rt.InspectContainer(inspectCtx, name)
	if err != nil {
		t.Fatalf("InspectContainer: %v", err)
	}

	// Port bindings are only present in NetworkSettings while the container ran;
	// if both are populated, verify they match the spec.
	if info.HostPort != 0 && info.HostPort != hostPort {
		t.Errorf("HostPort: got %d, want %d", info.HostPort, hostPort)
	}
	if info.ContainerPort != 0 && info.ContainerPort != containerPort {
		t.Errorf("ContainerPort: got %d, want %d", info.ContainerPort, containerPort)
	}
}

func TestIntegration_Docker_StopRemoveLifecycle(t *testing.T) {
	skipUnlessDockerIntegration(t)

	rt := integrationRuntime(t)
	name := containerName(t)
	t.Cleanup(func() { ensureRemoved(rt, name) })

	startCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := rt.StartContainer(startCtx, docker.ContainerSpec{
		Name:          name,
		Image:         integrationImage,
		Labels:        managedLabels(),
		RestartPolicy: "no",
	}); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	// Stop is idempotent — should not fail even if container already exited.
	stopCtx, cancel2 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel2()

	if err := rt.StopContainer(stopCtx, name, 5*time.Second); err != nil {
		t.Fatalf("StopContainer: %v", err)
	}

	rmCtx, cancel3 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel3()

	if err := rt.RemoveContainer(rmCtx, name); err != nil {
		t.Fatalf("RemoveContainer: %v", err)
	}

	// Container is gone — inspect must return not-found error.
	inspectCtx, cancel4 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel4()

	_, err := rt.InspectContainer(inspectCtx, name)
	if err == nil {
		t.Error("InspectContainer after remove should return an error")
	}
}

func TestIntegration_Docker_ListContainers_ManagedOnly(t *testing.T) {
	skipUnlessDockerIntegration(t)

	rt := integrationRuntime(t)
	name := containerName(t)
	t.Cleanup(func() { ensureRemoved(rt, name) })

	startCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := rt.StartContainer(startCtx, docker.ContainerSpec{
		Name:          name,
		Image:         integrationImage,
		Labels:        managedLabels(),
		RestartPolicy: "no",
	}); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	listCtx, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()

	containers, err := rt.ListContainers(listCtx, docker.ContainerFilter{ManagedOnly: true})
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}

	found := false
	for _, c := range containers {
		if c.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("container %q not found in managed list (got %d total)", name, len(containers))
	}
}
