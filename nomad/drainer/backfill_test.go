// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package drainer

import (
	"context"
	"testing"
	"time"

	"github.com/hashicorp/nomad/helper/testlog"
	"github.com/hashicorp/nomad/nomad/mock"
	"github.com/hashicorp/nomad/nomad/state"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/testutil"
	"github.com/shoenig/test/must"
)

type backfillRaft struct {
	*MockRaftApplierShim
	t      *testing.T
	nodeID string
}

func (r *backfillRaft) AllocUpdateDesiredTransition(allocs map[string]*structs.DesiredTransition, evals []*structs.Evaluation) (uint64, error) {
	node, err := r.state.NodeByID(nil, r.nodeID)
	must.NoError(r.t, err)
	must.True(r.t, node.DrainStrategy.BackfillClosed, must.Sprint("admission must close before stopping allocations"))
	return r.MockRaftApplierShim.AllocUpdateDesiredTransition(allocs, evals)
}

func TestDrainer_DurationAwareBackfill(t *testing.T) {
	t.Parallel()
	store := state.TestStateStore(t)
	node := mock.Node()
	node.SchedulingEligibility = structs.NodeSchedulingIneligible
	node.DrainStrategy = &structs.DrainStrategy{
		DrainSpec: structs.DrainSpec{Deadline: time.Hour, DurationAware: true}, ForceDeadline: time.Now().Add(time.Hour),
	}
	must.NoError(t, store.UpsertNode(structs.MsgTypeTestSetup, 100, node))
	d := &NodeDrainer{
		logger: testlog.HCLogger(t), state: store, nodes: make(map[string]*drainingNode),
		jobWatcher:       &MockJobWatcher{jobs: make(map[structs.NamespacedID]struct{})},
		deadlineNotifier: &MockDeadlineNotifier{nodes: make(map[string]struct{})},
		raft:             &backfillRaft{MockRaftApplierShim: &MockRaftApplierShim{state: store}, t: t, nodeID: node.ID},
	}
	d.Update(node)
	stored, err := store.NodeByID(nil, node.ID)
	must.NoError(t, err)
	must.NotNil(t, stored.DrainStrategy, must.Sprint("empty intervals must remain open for backfill"))

	// These jobs arrive after the original job watcher was registered. The final
	// scan must include them without early-stopping them at deadline - runtime.
	allocs := []*structs.Allocation{mock.BatchAlloc(), mock.SysBatchAlloc()}
	for i, alloc := range allocs {
		alloc.NodeID = node.ID
		alloc.Job.TaskGroups[0].MaxRunDuration = new(time.Minute)
		must.NoError(t, store.UpsertJob(structs.MsgTypeTestSetup, uint64(101+i), nil, alloc.Job))
	}
	must.NoError(t, store.UpsertAllocs(structs.MsgTypeTestSetup, 103, allocs))
	must.MapEmpty(t, d.jobWatcher.(*MockJobWatcher).jobs)
	for _, alloc := range allocs {
		stored, err := store.AllocByID(nil, alloc.ID)
		must.NoError(t, err)
		must.False(t, stored.DesiredTransition.ShouldMigrate())
	}
	d.handleDeadlinedNodes([]string{node.ID})
	for _, alloc := range allocs {
		stored, err := store.AllocByID(nil, alloc.ID)
		must.NoError(t, err)
		must.True(t, stored.DesiredTransition.ShouldMigrate())
	}
	stored, err = store.NodeByID(nil, node.ID)
	must.NoError(t, err)
	must.Nil(t, stored.DrainStrategy)
	must.Eq(t, structs.NodeSchedulingIneligible, stored.SchedulingEligibility)
}

func TestDrainer_DurationAwareResumeClosed(t *testing.T) {
	t.Parallel()
	store := state.TestStateStore(t)
	node := mock.Node()
	node.SchedulingEligibility = structs.NodeSchedulingIneligible
	node.DrainStrategy = &structs.DrainStrategy{
		DrainSpec:     structs.DrainSpec{Deadline: time.Hour, DurationAware: true},
		ForceDeadline: time.Now().Add(-time.Second), BackfillClosed: true,
	}
	must.NoError(t, store.UpsertNode(structs.MsgTypeTestSetup, 100, node))
	alloc := mock.BatchAlloc()
	alloc.NodeID = node.ID
	must.NoError(t, store.UpsertJob(structs.MsgTypeTestSetup, 101, nil, alloc.Job))
	must.NoError(t, store.UpsertAllocs(structs.MsgTypeTestSetup, 102, []*structs.Allocation{alloc}))
	d := NewNodeDrainer(&NodeDrainerConfig{
		Logger: testlog.HCLogger(t), Raft: &MockRaftApplierShim{state: store},
		JobFactory: GetDrainingJobWatcher, NodeFactory: GetNodeWatcherFactory(),
		DrainDeadlineFactory:  func(ctx context.Context) DrainDeadlineNotifier { return NewDeadlineHeap(ctx, time.Millisecond) },
		StateQueriesPerSecond: 100, BatchUpdateInterval: time.Millisecond,
	})
	d.SetEnabled(true, store)
	defer d.SetEnabled(false, nil)
	testutil.WaitForResult(func() (bool, error) {
		n, err := store.NodeByID(nil, node.ID)
		return err == nil && n.DrainStrategy == nil, err
	}, func(err error) { t.Fatal(err) })
	stored, err := store.AllocByID(nil, alloc.ID)
	must.NoError(t, err)
	must.True(t, stored.DesiredTransition.ShouldMigrate())
}
