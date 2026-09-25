// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package feasible

import (
	"testing"
	"time"

	"github.com/hashicorp/nomad/nomad/mock"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/shoenig/test/must"
)

func TestDrainBackfill_NotCachedByClass(t *testing.T) {
	t.Parallel()
	_, ctx := MockContext(t)
	job := mock.Job()
	job.Type = structs.JobTypeBatch
	job.TaskGroups[0].MaxRunDuration = new(time.Minute)
	short := mock.Node()
	short.DrainStrategy = &structs.DrainStrategy{
		DrainSpec: structs.DrainSpec{Deadline: time.Hour, DurationAware: true}, ForceDeadline: time.Now().Add(time.Hour),
	}
	expired := short.Copy()
	expired.ID = "expired"
	expired.DrainStrategy.ForceDeadline = time.Now().Add(-time.Second)
	checker := &DrainChecker{ctx: ctx, job: job, tg: job.TaskGroups[0]}
	ctx.Eligibility().SetJob(job)
	wrapper := NewFeasibilityWrapper(ctx, NewStaticIterator(ctx, []*structs.Node{expired, short}), nil, nil, []FeasibilityChecker{checker})
	wrapper.SetTaskGroup(job.TaskGroups[0].Name)
	must.Eq(t, short, wrapper.Next())
	must.Nil(t, wrapper.Next())
	// A cached eligible class must not bypass a later task group's time budget.
	checker.tg = job.TaskGroups[0].Copy()
	checker.tg.MaxRunDuration = new(2 * time.Hour)
	wrapper.Reset()
	must.Nil(t, wrapper.Next())
}

func TestDrainBackfill_NoPreemption(t *testing.T) {
	t.Parallel()
	store, ctx := MockContext(t)
	node := mock.Node()
	existing := mock.BatchAlloc()
	existing.NodeID = node.ID
	existing.Job.Priority = 1
	existing.AllocatedResources = structs.NodeResourcesToAllocatedResources(node.NodeResources)
	must.NoError(t, store.UpsertJob(structs.MsgTypeTestSetup, 99, nil, existing.Job))
	must.NoError(t, store.UpsertAllocs(structs.MsgTypeTestSetup, 100, []*structs.Allocation{existing}))
	job := mock.BatchJob()
	job.Priority = 100
	job.TaskGroups[0].MaxRunDuration = new(time.Minute)
	stack := NewGenericStack(true, ctx)
	stack.SetJob(job)
	stack.SetNodes([]*structs.Node{node})
	options := &SelectOptions{Preempt: true}
	selected := stack.Select(job.TaskGroups[0], options)
	must.NotNil(t, selected, must.Sprint("control: ordinary scheduling can preempt"))
	must.Len(t, 1, selected.PreemptedAllocs)
	node.DrainStrategy = &structs.DrainStrategy{
		DrainSpec: structs.DrainSpec{Deadline: time.Hour, DurationAware: true}, ForceDeadline: time.Now().Add(time.Hour),
	}
	must.Nil(t, stack.Select(job.TaskGroups[0], options), must.Sprint("backfill must not evict existing work"))
}
