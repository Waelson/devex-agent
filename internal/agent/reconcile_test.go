package agent

import (
	"context"
	"testing"
	"time"

	"devex-agent/internal/docker"
	"devex-agent/internal/state"
)

// newReconcileAgent returns a minimal RuntimeAgent wired for reconcileDeployments tests.
// It shares the fake implementations from command_processor_test.go (same package).
func newReconcileAgent(t *testing.T) *RuntimeAgent {
	t.Helper()
	a, _, _, _, ss, _ := newTestAgent(t)
	ss.localState = &state.LocalState{}
	a.localState = ss.localState
	return a
}

// ============================================================
// reconcileDeployments
// ============================================================

func TestReconcileDeployments_ActiveContainerRunning_PreservesStatus(t *testing.T) {
	a := newReconcileAgent(t)
	a.localState.Deployments = []state.DeploymentEntry{{
		DeploymentID:  "dep_1",
		ContainerName: "billing-api-test-v42",
		Status:        state.DeploymentStatusActive,
	}}

	containers := []docker.ContainerInfo{{
		Name: "billing-api-test-v42", Running: true, Status: "running",
	}}

	a.reconcileDeployments(context.Background(), containers)

	dep := findDeploymentByID(a.localState, "dep_1")
	if dep == nil {
		t.Fatal("deployment removed unexpectedly")
	}
	if dep.Status != state.DeploymentStatusActive {
		t.Errorf("status: got %q, want active", dep.Status)
	}
}

func TestReconcileDeployments_ActiveContainerAbsent_MarkedInconsistent(t *testing.T) {
	a := newReconcileAgent(t)
	a.localState.Deployments = []state.DeploymentEntry{{
		DeploymentID:  "dep_1",
		ContainerName: "billing-api-test-v42",
		Status:        state.DeploymentStatusActive,
	}}

	// No containers in Docker.
	a.reconcileDeployments(context.Background(), nil)

	dep := findDeploymentByID(a.localState, "dep_1")
	if dep == nil {
		t.Fatal("deployment should remain in state (as inconsistent)")
	}
	if dep.Status != state.DeploymentStatusInconsistent {
		t.Errorf("status: got %q, want inconsistent", dep.Status)
	}
	if dep.UpdatedAt.IsZero() {
		t.Error("UpdatedAt must be set when status changes")
	}
}

func TestReconcileDeployments_ActiveContainerStopped_MarkedInconsistent(t *testing.T) {
	a := newReconcileAgent(t)
	a.localState.Deployments = []state.DeploymentEntry{{
		DeploymentID:  "dep_1",
		ContainerName: "billing-api-test-v42",
		Status:        state.DeploymentStatusActive,
	}}

	// Container exists in Docker but is stopped.
	containers := []docker.ContainerInfo{{
		Name: "billing-api-test-v42", Running: false, Status: "exited",
	}}

	a.reconcileDeployments(context.Background(), containers)

	dep := findDeploymentByID(a.localState, "dep_1")
	if dep == nil {
		t.Fatal("deployment should remain in state")
	}
	if dep.Status != state.DeploymentStatusInconsistent {
		t.Errorf("status: got %q, want inconsistent", dep.Status)
	}
}

func TestReconcileDeployments_CheckingHealthContainerAbsent_MarkedInconsistent(t *testing.T) {
	a := newReconcileAgent(t)
	a.localState.Deployments = []state.DeploymentEntry{{
		DeploymentID:  "dep_1",
		ContainerName: "billing-api-test-v42",
		Status:        state.DeploymentStatusCheckingHealth,
	}}

	a.reconcileDeployments(context.Background(), nil)

	dep := findDeploymentByID(a.localState, "dep_1")
	if dep == nil || dep.Status != state.DeploymentStatusInconsistent {
		t.Errorf("checking_health container absent should be inconsistent, got %+v", dep)
	}
}

func TestReconcileDeployments_DrainingContainerAbsent_NotMarkedInconsistent(t *testing.T) {
	a := newReconcileAgent(t)
	past := time.Now().Add(-3 * time.Minute)
	a.localState.Deployments = []state.DeploymentEntry{{
		DeploymentID:      "dep_1",
		ContainerName:     "billing-api-test-v41",
		Status:            state.DeploymentStatusDraining,
		DrainingStartedAt: &past,
	}}

	// Draining container may already be stopped — that is normal.
	a.reconcileDeployments(context.Background(), nil)

	dep := findDeploymentByID(a.localState, "dep_1")
	if dep == nil {
		t.Fatal("deployment should remain in state")
	}
	if dep.Status != state.DeploymentStatusDraining {
		t.Errorf("draining container absent should stay draining, got %q", dep.Status)
	}
}

func TestReconcileDeployments_TerminalStatus_Skipped(t *testing.T) {
	a := newReconcileAgent(t)
	a.localState.Deployments = []state.DeploymentEntry{
		{DeploymentID: "dep_removed", ContainerName: "old-1", Status: state.DeploymentStatusRemoved},
		{DeploymentID: "dep_failed", ContainerName: "old-2", Status: state.DeploymentStatusFailed},
	}

	// No containers — terminal entries should not be touched.
	a.reconcileDeployments(context.Background(), nil)

	for _, depID := range []string{"dep_removed", "dep_failed"} {
		dep := findDeploymentByID(a.localState, depID)
		if dep == nil {
			t.Errorf("terminal deployment %q should remain in state", depID)
		}
	}
}

func TestReconcileDeployments_OrphanedContainer_DoesNotModifyState(t *testing.T) {
	a := newReconcileAgent(t)
	// No deployments in local state.

	containers := []docker.ContainerInfo{{
		Name: "mystery-container", Running: true, Status: "running",
	}}

	// Should not panic, should not add the orphan to state.
	a.reconcileDeployments(context.Background(), containers)

	if len(a.localState.Deployments) != 0 {
		t.Errorf("orphan must not be added to state, got %d entries", len(a.localState.Deployments))
	}
}

func TestReconcileDeployments_MultipleDeployments_PartialReconcile(t *testing.T) {
	a := newReconcileAgent(t)
	a.localState.Deployments = []state.DeploymentEntry{
		{DeploymentID: "dep_ok", ContainerName: "app-v2", Status: state.DeploymentStatusActive},
		{DeploymentID: "dep_missing", ContainerName: "app-v1", Status: state.DeploymentStatusActive},
	}

	// Only app-v2 is alive.
	containers := []docker.ContainerInfo{{
		Name: "app-v2", Running: true, Status: "running",
	}}

	a.reconcileDeployments(context.Background(), containers)

	depOK := findDeploymentByID(a.localState, "dep_ok")
	depMissing := findDeploymentByID(a.localState, "dep_missing")

	if depOK == nil || depOK.Status != state.DeploymentStatusActive {
		t.Errorf("dep_ok should stay active, got %+v", depOK)
	}
	if depMissing == nil || depMissing.Status != state.DeploymentStatusInconsistent {
		t.Errorf("dep_missing should be inconsistent, got %+v", depMissing)
	}
}

func TestReconcileDeployments_EmptyState_NoOp(t *testing.T) {
	a := newReconcileAgent(t)
	// Should not panic with empty state and containers.
	a.reconcileDeployments(context.Background(), nil)
	a.reconcileDeployments(context.Background(), []docker.ContainerInfo{})
}

// ============================================================
// isTerminalDeploymentStatus / isRunningExpectedStatus
// ============================================================

func TestIsTerminalDeploymentStatus(t *testing.T) {
	terminal := []state.DeploymentStatus{
		state.DeploymentStatusRemoved,
		state.DeploymentStatusFailed,
	}
	for _, s := range terminal {
		if !isTerminalDeploymentStatus(s) {
			t.Errorf("expected %q to be terminal", s)
		}
	}

	nonTerminal := []state.DeploymentStatus{
		state.DeploymentStatusActive,
		state.DeploymentStatusDraining,
		state.DeploymentStatusStarting,
		state.DeploymentStatusCheckingHealth,
		state.DeploymentStatusOrphaned,
		state.DeploymentStatusInconsistent,
	}
	for _, s := range nonTerminal {
		if isTerminalDeploymentStatus(s) {
			t.Errorf("expected %q to be non-terminal", s)
		}
	}
}

func TestIsRunningExpectedStatus(t *testing.T) {
	expected := []state.DeploymentStatus{
		state.DeploymentStatusActive,
		state.DeploymentStatusCheckingHealth,
		state.DeploymentStatusStarting,
	}
	for _, s := range expected {
		if !isRunningExpectedStatus(s) {
			t.Errorf("expected %q to require running container", s)
		}
	}

	notExpected := []state.DeploymentStatus{
		state.DeploymentStatusDraining,
		state.DeploymentStatusRemoved,
		state.DeploymentStatusFailed,
		state.DeploymentStatusOrphaned,
		state.DeploymentStatusInconsistent,
		state.DeploymentStatusReserved,
	}
	for _, s := range notExpected {
		if isRunningExpectedStatus(s) {
			t.Errorf("expected %q to NOT require running container", s)
		}
	}
}
