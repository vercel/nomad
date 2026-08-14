// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package nomad

import (
	"testing"
	"time"

	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/shoenig/test/must"
)

func TestJobGCTelemetry(t *testing.T) {
	periodic := newJobGCTelemetry(structs.CoreJobJobGC)
	must.Eq(t, "periodic", periodic.trigger)

	forced := newJobGCTelemetry(structs.CoreJobForceGC + ":requested")
	must.Eq(t, "force", forced.trigger)

	job := &structs.Job{
		Type:   structs.JobTypeBatch,
		Status: structs.JobStatusDead,
		Stop:   true,
	}
	periodic.examineJob(job)
	periodic.examineJob(job)
	periodic.examineEval(&structs.Evaluation{
		Type:   structs.JobTypeBatch,
		Status: structs.EvalStatusComplete,
	})
	periodic.retainJob(jobGCRetainAllocationBlocked)

	must.Eq(t, 2, periodic.jobsExamined)
	must.Eq(t, 2, periodic.jobStates[jobGCState{
		jobType: structs.JobTypeBatch,
		status:  structs.JobStatusDead,
		stopped: true,
	}])
	must.Eq(t, 1, periodic.evalsExamined)
	must.Eq(t, 1, periodic.evalStates[evalGCState{
		jobType: structs.JobTypeBatch,
		status:  structs.EvalStatusComplete,
	}])
	must.Eq(t, 1, periodic.jobsRetained[jobGCRetainAllocationBlocked])
}

func TestClassifyIneligibleEval(t *testing.T) {
	cutoff := time.Now().UTC()
	testCases := []struct {
		name     string
		eval     *structs.Evaluation
		expected string
	}{
		{
			name:     "non-terminal",
			eval:     &structs.Evaluation{Status: structs.EvalStatusPending},
			expected: jobGCRetainEvalNonTerminal,
		},
		{
			name: "too young",
			eval: &structs.Evaluation{
				Status:     structs.EvalStatusComplete,
				ModifyTime: cutoff.Add(time.Minute).UnixNano(),
			},
			expected: jobGCRetainEvalTooYoung,
		},
		{
			name: "allocation blocked",
			eval: &structs.Evaluation{
				Status:     structs.EvalStatusComplete,
				ModifyTime: cutoff.Add(-time.Minute).UnixNano(),
			},
			expected: jobGCRetainAllocationBlocked,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			must.Eq(t, tc.expected, classifyIneligibleEval(tc.eval, cutoff))
		})
	}
}

func TestPartitionCount(t *testing.T) {
	testCases := []struct {
		name      string
		objects   int
		batchSize int
		expected  int
	}{
		{name: "empty", objects: 0, batchSize: 10, expected: 0},
		{name: "partial batch", objects: 9, batchSize: 10, expected: 1},
		{name: "exact batch", objects: 10, batchSize: 10, expected: 1},
		{name: "multiple batches", objects: 21, batchSize: 10, expected: 3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			must.Eq(t, tc.expected, partitionCount(tc.objects, tc.batchSize))
		})
	}
}
