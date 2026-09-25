// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package nomad

import (
	"errors"
	"testing"
	"time"

	"github.com/hashicorp/nomad/helper/testlog"
	"github.com/hashicorp/nomad/nomad/mock"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/shoenig/test/must"
)

type rejectedBackfillFuture struct{}

func (rejectedBackfillFuture) Error() error  { return nil }
func (rejectedBackfillFuture) Index() uint64 { return 100 }
func (rejectedBackfillFuture) Response() any { return errors.New("stale backfill fence") }

func TestPlanApply_BackfillFSMRejection(t *testing.T) {
	t.Parallel()
	p := &planner{srv: &Server{logger: testlog.HCLogger(t)}}
	pending := &pendingPlan{errCh: make(chan error, 1)}
	indexes := make(chan uint64, 1)
	p.asyncPlanWait(indexes, rejectedBackfillFuture{}, &structs.PlanResult{}, pending)
	result, err := pending.Wait()
	must.Error(t, err)
	must.Nil(t, result)
	must.Eq(t, uint64(0), <-indexes)
}

func TestPlanApply_DrainBackfill(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		existing bool
		duration time.Duration
		want     bool
	}{
		{"new short", false, time.Minute, true},
		{"new long", false, 2 * time.Hour, false},
		{"new unbounded", false, 0, false},
		{"in-place unchanged", true, time.Minute, true},
		{"in-place extension fits", true, 2 * time.Minute, true},
		{"in-place extension exceeds deadline", true, 2 * time.Hour, false},
		{"in-place removes limit", true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStateStore(t)
			node := mock.Node()
			node.SchedulingEligibility = structs.NodeSchedulingIneligible
			node.DrainStrategy = &structs.DrainStrategy{
				DrainSpec: structs.DrainSpec{Deadline: time.Hour, DurationAware: true}, ForceDeadline: time.Now().Add(time.Hour),
			}
			must.NoError(t, s.UpsertNode(structs.MsgTypeTestSetup, 100, node))
			alloc := mock.BatchAlloc()
			alloc.NodeID = node.ID
			alloc.CreateTime = time.Now().UnixNano()
			alloc.Job.TaskGroups[0].MaxRunDuration = new(time.Minute)
			must.NoError(t, s.UpsertJob(structs.MsgTypeTestSetup, 101, nil, alloc.Job))
			if tc.existing {
				must.NoError(t, s.UpsertAllocs(structs.MsgTypeTestSetup, 102, []*structs.Allocation{alloc}))
			}
			updated := alloc.Copy()
			job := updated.Job
			job.TaskGroups[0].MaxRunDuration = nil
			if tc.duration > 0 {
				job.TaskGroups[0].MaxRunDuration = new(tc.duration)
			}
			updated.Job = nil // exercise normalized scheduler plans
			plan := &structs.Plan{Job: job, NodeAllocation: map[string][]*structs.Allocation{node.ID: {updated}}}
			snap, err := s.Snapshot()
			must.NoError(t, err)
			fit, _, err := evaluateNodePlan(snap, plan, node.ID)
			must.NoError(t, err)
			must.Eq(t, tc.want, fit)
		})
	}
}
