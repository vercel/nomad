// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package structs

import (
	"math"
	"testing"
	"time"

	"github.com/shoenig/test/must"
)

func TestDrainBackfillAdmission(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	for _, tc := range []struct {
		name   string
		change func(*Node, *Job, *TaskGroup)
		want   bool
	}{
		{"fits", func(*Node, *Job, *TaskGroup) {}, true},
		{"sysbatch", func(_ *Node, j *Job, _ *TaskGroup) { j.Type = JobTypeSysBatch }, true},
		{"unbounded", func(_ *Node, _ *Job, tg *TaskGroup) { tg.MaxRunDuration = nil }, false},
		{"zero runtime", func(_ *Node, _ *Job, tg *TaskGroup) { tg.MaxRunDuration = new(time.Duration(0)) }, false},
		{"service", func(_ *Node, j *Job, _ *TaskGroup) { j.Type = JobTypeService }, false},
		{"ordinary drain", func(n *Node, _ *Job, _ *TaskGroup) { n.DrainStrategy.DurationAware = false }, false},
		{"closed", func(n *Node, _ *Job, _ *TaskGroup) { n.DrainStrategy.BackfillClosed = true }, false},
		{"down", func(n *Node, _ *Job, _ *TaskGroup) { n.Status = NodeStatusDown }, false},
		{"disconnected", func(n *Node, _ *Job, _ *TaskGroup) { n.Status = NodeStatusDisconnected }, false},
		{"infinite", func(n *Node, _ *Job, _ *TaskGroup) { n.DrainStrategy.Deadline = 0 }, false},
		{"forced", func(n *Node, _ *Job, _ *TaskGroup) { n.DrainStrategy.Deadline = -1 }, false},
		{"missing deadline", func(n *Node, _ *Job, _ *TaskGroup) { n.DrainStrategy.ForceDeadline = time.Time{} }, false},
		{"expired", func(n *Node, _ *Job, _ *TaskGroup) { n.DrainStrategy.ForceDeadline = now }, false},
		{"exact boundary", func(n *Node, _ *Job, _ *TaskGroup) { n.DrainStrategy.ForceDeadline = now.Add(90 * time.Second) }, false},
		{"just inside", func(n *Node, _ *Job, _ *TaskGroup) {
			n.DrainStrategy.ForceDeadline = now.Add(90*time.Second + time.Nanosecond)
		}, true},
		{"shutdown allowance", func(_ *Node, _ *Job, tg *TaskGroup) { tg.Tasks[0].KillTimeout = time.Minute }, false},
		{"buffer", func(n *Node, _ *Job, _ *TaskGroup) { n.DrainStrategy.BackfillBuffer = time.Minute }, false},
		{"overflow", func(_ *Node, _ *Job, tg *TaskGroup) { tg.MaxRunDuration = new(time.Duration(math.MaxInt64)) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tg := &TaskGroup{Name: "work", MaxRunDuration: new(time.Minute), Tasks: []*Task{{Name: "task"}}}
			job := &Job{Type: JobTypeBatch, TaskGroups: []*TaskGroup{tg}}
			node := &Node{Status: NodeStatusReady, DrainStrategy: &DrainStrategy{
				DrainSpec: DrainSpec{Deadline: 2 * time.Minute, DurationAware: true}, ForceDeadline: now.Add(2 * time.Minute),
			}}
			tc.change(node, job, tg)
			must.Eq(t, tc.want, node.CanBackfill(job, tg, now))
		})
	}
	// Budgets saturate instead of wrapping into an admissible negative value.
	tg := &TaskGroup{ShutdownDelay: new(time.Duration(math.MaxInt64)), Tasks: []*Task{{KillTimeout: time.Second}}}
	must.Eq(t, time.Duration(math.MaxInt64), tg.BackfillShutdownAllowance())
}

func TestDrainBackfillUpdates(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	tg := &TaskGroup{Name: "work", MaxRunDuration: new(time.Minute)}
	existing := &Allocation{Job: &Job{Type: JobTypeBatch, TaskGroups: []*TaskGroup{tg}}, TaskGroup: tg.Name,
		CreateTime: now.Add(-50 * time.Second).UnixNano(), DesiredStatus: AllocDesiredStatusRun}
	node := &Node{Status: NodeStatusReady, DrainStrategy: &DrainStrategy{
		DrainSpec: DrainSpec{Deadline: time.Minute, DurationAware: true}, ForceDeadline: now.Add(time.Minute),
	}}
	updated := existing.Copy()
	updated.Job.TaskGroups[0].MaxRunDuration = new(70 * time.Second)
	// An increase anchored to creation time fits, even though a fresh 70s job wouldn't.
	must.True(t, node.BackfillUpdateAllowed(existing, updated, now))
	must.False(t, node.CanBackfill(updated.Job, updated.Job.TaskGroups[0], now))
	updated.Job.TaskGroups[0].MaxRunDuration = new(2 * time.Minute)
	must.False(t, node.BackfillUpdateAllowed(existing, updated, now))
	updated.Job.TaskGroups[0].MaxRunDuration = nil
	must.False(t, node.BackfillUpdateAllowed(existing, updated, now))
	updated.DesiredStatus = AllocDesiredStatusStop
	must.True(t, node.BackfillUpdateAllowed(existing, updated, now))
	// Unrelated updates remain valid even after admission closes.
	node.DrainStrategy.BackfillClosed = true
	must.True(t, node.BackfillUpdateAllowed(existing, existing.Copy(), now))
}

func TestDrainSpecBackfillValidation(t *testing.T) {
	t.Parallel()
	for _, d := range []*DrainSpec{
		{DurationAware: true}, {DurationAware: true, Deadline: -time.Second},
		{DurationAware: true, Deadline: time.Hour, BackfillBuffer: -1}, {BackfillBuffer: time.Second},
	} {
		must.Error(t, d.Validate())
	}
	must.NoError(t, (&DrainSpec{DurationAware: true, Deadline: time.Hour}).Validate())
	must.NoError(t, (&DrainSpec{}).Validate())
}
