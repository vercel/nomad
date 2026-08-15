// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package nomad

import (
	"strconv"
	"strings"
	"time"

	metrics "github.com/hashicorp/go-metrics/compat"
	"github.com/hashicorp/nomad/nomad/structs"
)

var (
	jobGCMetricRunsStarted   = []string{"nomad", "core_scheduler", "job_gc", "runs_started"}
	jobGCMetricRunsCompleted = []string{"nomad", "core_scheduler", "job_gc", "runs_completed"}
	jobGCMetricDuration      = []string{"nomad", "core_scheduler", "job_gc", "duration"}
	jobGCMetricPhaseDuration = []string{"nomad", "core_scheduler", "job_gc", "phase_duration"}
	jobGCMetricObjects       = []string{"nomad", "core_scheduler", "job_gc", "objects_per_run"}
	jobGCMetricObjectsCount  = []string{"nomad", "core_scheduler", "job_gc", "objects"}
	jobGCMetricJobState      = []string{"nomad", "core_scheduler", "job_gc", "job_state"}
	jobGCMetricEvalState     = []string{"nomad", "core_scheduler", "job_gc", "eval_state"}
	jobGCMetricJobsRetained  = []string{"nomad", "core_scheduler", "job_gc", "jobs_retained_per_run"}
	jobGCMetricRetainedCount = []string{"nomad", "core_scheduler", "job_gc", "jobs_retained"}
	jobGCMetricRPCRequests   = []string{"nomad", "core_scheduler", "job_gc", "rpc_requests"}
	jobGCMetricErrors        = []string{"nomad", "core_scheduler", "job_gc", "errors"}
)

const (
	jobGCPhaseScan     = "scan"
	jobGCPhaseEvalReap = "eval_reap"
	jobGCPhaseJobReap  = "job_reap"

	jobGCRetainTooYoung           = "too_young"
	jobGCRetainEvalLookupError    = "eval_lookup_error"
	jobGCRetainEvalError          = "eval_error"
	jobGCRetainEvalNonTerminal    = "eval_non_terminal"
	jobGCRetainEvalTooYoung       = "eval_too_young"
	jobGCRetainAllocationBlocked  = "allocation_blocked"
	jobGCRetainVersionLookupError = "version_lookup_error"
	jobGCRetainVersionTagged      = "version_tagged"
)

type jobGCState struct {
	jobType string
	status  string
	stopped bool
}

type evalGCState struct {
	jobType string
	status  string
}

type jobGCTelemetry struct {
	start   time.Time
	trigger string

	jobsExamined  int
	evalsExamined int
	jobStates     map[jobGCState]int
	evalStates    map[evalGCState]int
	jobsRetained  map[string]int

	jobsEligible   int
	evalsEligible  int
	allocsEligible int
	jobsReaped     int
	evalsReaped    int
	allocsReaped   int

	evalReapRequests int
	jobReapRequests  int
	errorPhase       string
}

func newJobGCTelemetry(evalJobID string) *jobGCTelemetry {
	trigger := "periodic"
	if strings.Split(evalJobID, ":")[0] == structs.CoreJobForceGC {
		trigger = "force"
	}
	return &jobGCTelemetry{
		start:        time.Now(),
		trigger:      trigger,
		jobStates:    make(map[jobGCState]int),
		evalStates:   make(map[evalGCState]int),
		jobsRetained: make(map[string]int),
	}
}

func (t *jobGCTelemetry) examineEval(eval *structs.Evaluation) {
	t.evalsExamined++
	t.evalStates[evalGCState{
		jobType: eval.Type,
		status:  eval.Status,
	}]++
}

func (t *jobGCTelemetry) examineJob(job *structs.Job) {
	t.jobsExamined++
	t.jobStates[jobGCState{
		jobType: job.Type,
		status:  job.Status,
		stopped: job.Stop,
	}]++
}

func (t *jobGCTelemetry) retainJob(reason string) {
	t.jobsRetained[reason]++
}

func (t *jobGCTelemetry) runStarted() {
	metrics.IncrCounterWithLabels(jobGCMetricRunsStarted, 1,
		[]metrics.Label{{Name: "trigger", Value: t.trigger}})
}

func (t *jobGCTelemetry) measurePhaseSince(phase string, start time.Time) {
	metrics.MeasureSinceWithLabels(jobGCMetricPhaseDuration, start, []metrics.Label{
		{Name: "phase", Value: phase},
		{Name: "trigger", Value: t.trigger},
	})
}

func (t *jobGCTelemetry) emit(runErr error) {
	result := "success"
	if runErr != nil {
		result = "error"
	}
	runLabels := []metrics.Label{
		{Name: "trigger", Value: t.trigger},
		{Name: "result", Value: result},
	}
	metrics.IncrCounterWithLabels(jobGCMetricRunsCompleted, 1, runLabels)
	metrics.MeasureSinceWithLabels(jobGCMetricDuration, t.start, runLabels)

	t.emitObjects("examined", "job", t.jobsExamined)
	t.emitObjects("examined", "evaluation", t.evalsExamined)
	t.emitObjects("eligible", "job", t.jobsEligible)
	t.emitObjects("eligible", "evaluation", t.evalsEligible)
	t.emitObjects("eligible", "allocation", t.allocsEligible)
	t.emitObjects("reaped", "job", t.jobsReaped)
	t.emitObjects("reaped", "evaluation", t.evalsReaped)
	t.emitObjects("reaped", "allocation", t.allocsReaped)

	for state, count := range t.jobStates {
		metrics.AddSampleWithLabels(jobGCMetricJobState, float32(count), []metrics.Label{
			{Name: "type", Value: state.jobType},
			{Name: "status", Value: state.status},
			{Name: "stopped", Value: strconv.FormatBool(state.stopped)},
			{Name: "trigger", Value: t.trigger},
		})
	}
	for state, count := range t.evalStates {
		metrics.AddSampleWithLabels(jobGCMetricEvalState, float32(count), []metrics.Label{
			{Name: "type", Value: state.jobType},
			{Name: "status", Value: state.status},
			{Name: "trigger", Value: t.trigger},
		})
	}
	for reason, count := range t.jobsRetained {
		labels := []metrics.Label{
			{Name: "reason", Value: reason},
			{Name: "trigger", Value: t.trigger},
		}
		metrics.AddSampleWithLabels(jobGCMetricJobsRetained, float32(count), labels)
		metrics.IncrCounterWithLabels(jobGCMetricRetainedCount, float32(count), labels)
	}

	t.emitRPCRequests(jobGCPhaseEvalReap, t.evalReapRequests)
	t.emitRPCRequests(jobGCPhaseJobReap, t.jobReapRequests)
	if t.errorPhase != "" {
		metrics.IncrCounterWithLabels(jobGCMetricErrors, 1, []metrics.Label{
			{Name: "phase", Value: t.errorPhase},
			{Name: "trigger", Value: t.trigger},
		})
	}
}

func classifyIneligibleEval(eval *structs.Evaluation, cutoffTime time.Time) string {
	if !eval.TerminalStatus() {
		return jobGCRetainEvalNonTerminal
	}
	if time.Unix(0, eval.ModifyTime).UTC().After(cutoffTime) {
		return jobGCRetainEvalTooYoung
	}
	return jobGCRetainAllocationBlocked
}

func (t *jobGCTelemetry) emitObjects(state, object string, count int) {
	labels := []metrics.Label{
		{Name: "state", Value: state},
		{Name: "object", Value: object},
		{Name: "trigger", Value: t.trigger},
	}
	metrics.AddSampleWithLabels(jobGCMetricObjects, float32(count), labels)
	if count > 0 {
		metrics.IncrCounterWithLabels(jobGCMetricObjectsCount, float32(count), labels)
	}
}

func (t *jobGCTelemetry) emitRPCRequests(phase string, count int) {
	metrics.AddSampleWithLabels(jobGCMetricRPCRequests, float32(count), []metrics.Label{
		{Name: "phase", Value: phase},
		{Name: "trigger", Value: t.trigger},
	})
}

func partitionCount(objects, batchSize int) int {
	if objects == 0 {
		return 0
	}
	return (objects + batchSize - 1) / batchSize
}
