// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package scheduler

import (
	"testing"
	"time"

	"github.com/hashicorp/nomad/nomad/mock"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/scheduler/tests"
	"github.com/shoenig/test/must"
)

func TestScheduler_DrainBackfill(t *testing.T) {
	t.Parallel()
	for _, jobType := range []string{structs.JobTypeBatch, structs.JobTypeSysBatch, structs.JobTypeService, structs.JobTypeSystem} {
		for _, tc := range []struct {
			name             string
			duration         time.Duration
			closed, ordinary bool
			want             int
		}{
			{"short", time.Minute, false, false, 1},
			{"long", 2 * time.Hour, false, false, 0},
			{"unbounded", 0, false, false, 0},
			{"closed", time.Minute, true, false, 0},
			{"ordinary", time.Minute, false, true, 0},
		} {
			t.Run(jobType+"/"+tc.name, func(t *testing.T) {
				h := tests.NewHarness(t)
				node := mock.Node()
				node.SchedulingEligibility = structs.NodeSchedulingIneligible
				node.DrainStrategy = &structs.DrainStrategy{
					DrainSpec:     structs.DrainSpec{Deadline: time.Hour, DurationAware: !tc.ordinary},
					ForceDeadline: time.Now().Add(time.Hour), BackfillClosed: tc.closed,
				}
				must.NoError(t, h.State.UpsertNode(structs.MsgTypeTestSetup, h.NextIndex(), node))
				job := mock.Job()
				scheduler := NewBatchScheduler
				switch jobType {
				case structs.JobTypeSysBatch:
					job = mock.SystemBatchJob()
					scheduler = NewSysBatchScheduler
				case structs.JobTypeService:
					scheduler = NewServiceScheduler
				case structs.JobTypeSystem:
					job = mock.SystemJob()
					scheduler = NewSystemScheduler
				}
				job.Type = jobType
				job.TaskGroups[0].Count = 1
				if tc.duration > 0 {
					job.TaskGroups[0].MaxRunDuration = new(tc.duration)
				}
				must.NoError(t, h.State.UpsertJob(structs.MsgTypeTestSetup, h.NextIndex(), nil, job))
				eval := mock.Eval()
				eval.TriggeredBy = structs.EvalTriggerJobRegister
				eval.JobID = job.ID
				eval.Type = job.Type
				must.NoError(t, h.State.UpsertEvals(structs.MsgTypeTestSetup, h.NextIndex(), []*structs.Evaluation{eval}))
				must.NoError(t, h.Process(scheduler, eval))
				allocs, err := h.State.AllocsByNode(nil, node.ID)
				must.NoError(t, err)
				want := tc.want
				if jobType == structs.JobTypeService || jobType == structs.JobTypeSystem {
					want = 0
				}
				must.Len(t, want, allocs)
			})
		}
	}
}
