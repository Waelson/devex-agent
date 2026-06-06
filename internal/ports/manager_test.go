package ports

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	aerrors "devex-agent/internal/errors"
)

// --- helpers ---

func newManager(t *testing.T, from, to, maxActive int) *Manager {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ports.json")
	m, err := NewManager(from, to, maxActive, path)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func mustAllocate(t *testing.T, m *Manager, spec AllocateSpec) int {
	t.Helper()
	port, err := m.Allocate(context.Background(), spec)
	if err != nil {
		t.Fatalf("Allocate(%+v): %v", spec, err)
	}
	return port
}

func assertErrorCode(t *testing.T, err error, code aerrors.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %q, got nil", code)
	}
	got := aerrors.CodeOf(err)
	if got != code {
		t.Errorf("error code: got %q, want %q (error: %v)", got, code, err)
	}
}

// --- NewManager ---

func TestNewManager_InvalidRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ports.json")
	_, err := NewManager(4114, 4100, 10, path)
	assertErrorCode(t, err, aerrors.CodePortAllocationFailed)
}

func TestNewManager_ZeroMaxActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ports.json")
	_, err := NewManager(4100, 4114, 0, path)
	assertErrorCode(t, err, aerrors.CodePortAllocationFailed)
}

func TestNewManager_ValidRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ports.json")
	m, err := NewManager(4100, 4114, 10, path)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil Manager")
	}
}

// --- Allocate ---

func TestAllocate_ReturnsFirstAvailablePort(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	port := mustAllocate(t, m, AllocateSpec{
		DeploymentID:          "dep_001",
		Application:           "billing-api",
		ContainerName:         "billing-api-dev-v1",
		ContainerInternalPort: 3000,
	})
	if port != 4100 {
		t.Errorf("expected first port 4100, got %d", port)
	}
}

func TestAllocate_AllocatesSequentially(t *testing.T) {
	m := newManager(t, 4100, 4104, 10)

	ports := make([]int, 3)
	for i := range ports {
		p := mustAllocate(t, m, AllocateSpec{
			DeploymentID:  "dep_00" + string(rune('1'+i)),
			ContainerName: "app-v" + string(rune('1'+i)),
		})
		ports[i] = p
	}

	if ports[0] != 4100 || ports[1] != 4101 || ports[2] != 4102 {
		t.Errorf("expected 4100,4101,4102; got %v", ports)
	}
}

func TestAllocate_NoDuplicatePorts(t *testing.T) {
	m := newManager(t, 4100, 4110, 10)

	seen := map[int]bool{}
	for i := 0; i < 5; i++ {
		p := mustAllocate(t, m, AllocateSpec{
			DeploymentID:  "dep_" + string(rune('A'+i)),
			ContainerName: "app-v" + string(rune('A'+i)),
		})
		if seen[p] {
			t.Errorf("duplicate port %d allocated", p)
		}
		seen[p] = true
	}
}

func TestAllocate_Idempotent_SameDeploymentID(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)

	spec := AllocateSpec{DeploymentID: "dep_001", ContainerName: "billing-api-dev-v1"}
	first := mustAllocate(t, m, spec)
	second := mustAllocate(t, m, spec)

	if first != second {
		t.Errorf("idempotency violated: first=%d second=%d", first, second)
	}

	// Ensure only one port was consumed.
	state, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(state.Allocations) != 1 {
		t.Errorf("expected 1 allocation, got %d", len(state.Allocations))
	}
}

func TestMarkActive_MaxActiveLimitReached(t *testing.T) {
	m := newManager(t, 4100, 4110, 2) // maxActive = 2

	// Allocate two ports and mark them active → limit reached.
	p1 := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_001", ContainerName: "app-v1"})
	p2 := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_002", ContainerName: "app-v2"})
	if err := m.MarkActive(p1); err != nil {
		t.Fatalf("MarkActive p1: %v", err)
	}
	if err := m.MarkActive(p2); err != nil {
		t.Fatalf("MarkActive p2: %v", err)
	}

	// Third port can still be allocated as reserved (blue/green allows it).
	p3 := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_003", ContainerName: "app-v3"})
	if p3 == 0 {
		t.Fatal("expected allocation to succeed for reserved port")
	}

	// But marking it active must fail: active limit already reached.
	err := m.MarkActive(p3)
	assertErrorCode(t, err, aerrors.CodePortAllocationFailed)
}

func TestAllocate_ReservedPortsDoNotCountAgainstActiveLimit(t *testing.T) {
	m := newManager(t, 4100, 4110, 1) // maxActive = 1

	// Allocate and mark active → limit hit.
	p1 := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_001", ContainerName: "app-v1"})
	if err := m.MarkActive(p1); err != nil {
		t.Fatalf("MarkActive: %v", err)
	}

	// A new allocation for a blue/green deploy should still succeed because
	// the new port starts as reserved (not active).
	_, err := m.Allocate(context.Background(), AllocateSpec{DeploymentID: "dep_002", ContainerName: "app-v2"})
	if err != nil {
		t.Fatalf("second allocation should succeed (reserved doesn't count as active): %v", err)
	}
}

func TestAllocate_RangeExhausted(t *testing.T) {
	m := newManager(t, 4100, 4101, 10) // only 2 ports

	mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_001", ContainerName: "app-v1"})
	mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_002", ContainerName: "app-v2"})

	_, err := m.Allocate(context.Background(), AllocateSpec{DeploymentID: "dep_003", ContainerName: "app-v3"})
	assertErrorCode(t, err, aerrors.CodePortRangeExhausted)
}

// --- MarkActive ---

func TestMarkActive_Success(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	port := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_001", ContainerName: "app-v1"})

	if err := m.MarkActive(port); err != nil {
		t.Fatalf("MarkActive: %v", err)
	}

	state, _ := m.Snapshot()
	alloc := state.Allocations[portKey(port)]
	if alloc.Status != PortStatusActive {
		t.Errorf("status: got %q, want active", alloc.Status)
	}
}

func TestMarkActive_AlreadyActive_NoError(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	port := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_001", ContainerName: "app-v1"})
	if err := m.MarkActive(port); err != nil {
		t.Fatal(err)
	}
	// Calling again should succeed (idempotent transition).
	if err := m.MarkActive(port); err != nil {
		t.Fatalf("second MarkActive: %v", err)
	}
}

func TestMarkActive_UnknownPort(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	err := m.MarkActive(4109)
	assertErrorCode(t, err, aerrors.CodePortStateInconsistent)
}

func TestMarkActive_FromDraining_Fails(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	port := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_001", ContainerName: "app-v1"})
	if err := m.MarkActive(port); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDraining(port); err != nil {
		t.Fatal(err)
	}
	err := m.MarkActive(port)
	assertErrorCode(t, err, aerrors.CodePortStateInconsistent)
}

// --- MarkDraining ---

func TestMarkDraining_Success(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	port := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_001", ContainerName: "app-v1"})
	if err := m.MarkActive(port); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDraining(port); err != nil {
		t.Fatalf("MarkDraining: %v", err)
	}

	state, _ := m.Snapshot()
	alloc := state.Allocations[portKey(port)]
	if alloc.Status != PortStatusDraining {
		t.Errorf("status: got %q, want draining", alloc.Status)
	}
	if alloc.DrainingStartedAt == nil {
		t.Error("DrainingStartedAt must be set")
	}
}

func TestMarkDraining_SetsDrainingStartedAt(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	before := time.Now().Add(-time.Second)
	port := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_001", ContainerName: "app-v1"})
	if err := m.MarkActive(port); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDraining(port); err != nil {
		t.Fatal(err)
	}

	state, _ := m.Snapshot()
	alloc := state.Allocations[portKey(port)]
	if alloc.DrainingStartedAt.Before(before) {
		t.Error("DrainingStartedAt should be recent")
	}
}

func TestMarkDraining_UnknownPort(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	err := m.MarkDraining(4105)
	assertErrorCode(t, err, aerrors.CodePortStateInconsistent)
}

func TestMarkDraining_FromReserved_Fails(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	port := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_001", ContainerName: "app-v1"})
	err := m.MarkDraining(port) // still reserved, not active
	assertErrorCode(t, err, aerrors.CodePortStateInconsistent)
}

// --- Release ---

func TestRelease_Success(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	port := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_001", ContainerName: "app-v1"})

	if err := m.Release(port); err != nil {
		t.Fatalf("Release: %v", err)
	}

	state, _ := m.Snapshot()
	if _, exists := state.Allocations[portKey(port)]; exists {
		t.Error("released port should not appear in allocations")
	}
}

func TestRelease_UnknownPort(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	err := m.Release(4100)
	assertErrorCode(t, err, aerrors.CodePortStateInconsistent)
}

func TestRelease_MakesPortAvailableForReuse(t *testing.T) {
	m := newManager(t, 4100, 4100, 10) // single-port range

	p1 := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_001", ContainerName: "app-v1"})
	if p1 != 4100 {
		t.Fatalf("expected 4100, got %d", p1)
	}

	if err := m.Release(p1); err != nil {
		t.Fatal(err)
	}

	// Should be re-allocatable now.
	p2 := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_002", ContainerName: "app-v2"})
	if p2 != 4100 {
		t.Errorf("expected 4100 after release, got %d", p2)
	}
}

// --- CountActive ---

func TestCountActive(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)

	p1 := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_001", ContainerName: "a1"})
	p2 := mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_002", ContainerName: "a2"})
	mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_003", ContainerName: "a3"}) // stays reserved

	if err := m.MarkActive(p1); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkActive(p2); err != nil {
		t.Fatal(err)
	}

	n, err := m.CountActive()
	if err != nil {
		t.Fatalf("CountActive: %v", err)
	}
	if n != 2 {
		t.Errorf("CountActive: got %d, want 2", n)
	}
}

// --- Reconcile ---

func TestReconcile_ReleasesPortForMissingContainer(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	port := mustAllocate(t, m, AllocateSpec{
		DeploymentID:  "dep_001",
		ContainerName: "billing-api-dev-v1",
	})
	if err := m.MarkActive(port); err != nil {
		t.Fatal(err)
	}

	// Reconcile with empty live-container set → container is gone.
	events, err := m.Reconcile(context.Background(), map[string]bool{})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Port != port {
		t.Errorf("event port: got %d, want %d", events[0].Port, port)
	}
	if events[0].OldStatus != PortStatusActive {
		t.Errorf("old status: got %q, want active", events[0].OldStatus)
	}

	// Port must be freed.
	state, _ := m.Snapshot()
	if _, exists := state.Allocations[portKey(port)]; exists {
		t.Error("port must be removed from allocations after reconcile")
	}
}

func TestReconcile_KeepsPortForLiveContainer(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	port := mustAllocate(t, m, AllocateSpec{
		DeploymentID:  "dep_001",
		ContainerName: "billing-api-dev-v1",
	})
	if err := m.MarkActive(port); err != nil {
		t.Fatal(err)
	}

	events, err := m.Reconcile(context.Background(), map[string]bool{
		"billing-api-dev-v1": true,
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for live container, got %d", len(events))
	}

	state, _ := m.Snapshot()
	alloc := state.Allocations[portKey(port)]
	if alloc == nil || alloc.Status != PortStatusActive {
		t.Error("allocation for live container must remain active")
	}
}

func TestReconcile_CleansUpFailedAndReleasedPorts(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	port := mustAllocate(t, m, AllocateSpec{
		DeploymentID:  "dep_001",
		ContainerName: "billing-api-dev-v1",
	})

	// Manually inject a failed allocation (simulating a crash mid-deploy).
	state, err := loadPortState(m.path)
	if err != nil {
		t.Fatal(err)
	}
	state.Allocations[portKey(port)].Status = PortStatusFailed
	if err := savePortState(m.path, state); err != nil {
		t.Fatal(err)
	}

	events, err := m.Reconcile(context.Background(), map[string]bool{})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event for failed port, got %d", len(events))
	}
	if events[0].OldStatus != PortStatusFailed {
		t.Errorf("old status: got %q, want failed", events[0].OldStatus)
	}
}

func TestReconcile_DrainedContainerGone_PortFreed(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	port := mustAllocate(t, m, AllocateSpec{
		DeploymentID:  "dep_001",
		ContainerName: "billing-api-dev-v1",
	})
	if err := m.MarkActive(port); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDraining(port); err != nil {
		t.Fatal(err)
	}

	// Container removed from Docker while draining.
	events, err := m.Reconcile(context.Background(), map[string]bool{})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(events) != 1 || events[0].OldStatus != PortStatusDraining {
		t.Errorf("expected draining→available event, got %+v", events)
	}
}

func TestReconcile_NoChanges_NoSave(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	port := mustAllocate(t, m, AllocateSpec{
		DeploymentID:  "dep_001",
		ContainerName: "live-container",
	})
	if err := m.MarkActive(port); err != nil {
		t.Fatal(err)
	}

	events, err := m.Reconcile(context.Background(), map[string]bool{"live-container": true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected no events, got %+v", events)
	}
}

// --- Persistence ---

func TestPersistence_StateRestoredAfterRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ports.json")

	m1, _ := NewManager(4100, 4114, 10, path)
	port := mustAllocate(t, m1, AllocateSpec{DeploymentID: "dep_001", ContainerName: "app-v1"})
	if err := m1.MarkActive(port); err != nil {
		t.Fatal(err)
	}

	// Simulate restart: create a new Manager with the same path.
	m2, err := NewManager(4100, 4114, 10, path)
	if err != nil {
		t.Fatalf("NewManager restart: %v", err)
	}

	state, err := m2.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot after restart: %v", err)
	}

	alloc := state.Allocations[portKey(port)]
	if alloc == nil {
		t.Fatal("allocation not found after restart")
	}
	if alloc.Status != PortStatusActive {
		t.Errorf("status after restart: got %q, want active", alloc.Status)
	}
	if alloc.DeploymentID != "dep_001" {
		t.Errorf("DeploymentID: got %q", alloc.DeploymentID)
	}
}

func TestPersistence_AtomicWrite_NoTmpFileRemains(t *testing.T) {
	m := newManager(t, 4100, 4114, 10)
	mustAllocate(t, m, AllocateSpec{DeploymentID: "dep_001", ContainerName: "app-v1"})

	tmpPath := m.path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temporary file must not exist after successful write")
	}
}

func TestPortStatusConstants_NonEmpty(t *testing.T) {
	statuses := []PortStatus{
		PortStatusAvailable,
		PortStatusReserved,
		PortStatusActive,
		PortStatusDraining,
		PortStatusFailed,
		PortStatusReleased,
		PortStatusUnmanaged,
	}
	for _, s := range statuses {
		if s == "" {
			t.Error("port status constant must not be empty")
		}
	}
}
