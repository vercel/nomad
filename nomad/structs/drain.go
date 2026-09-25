// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package structs

import (
	"fmt"
	"math"
	"time"
)

const DefaultBackfillBuffer = 30 * time.Second

func (d *DrainSpec) Validate() error {
	if d.DurationAware && d.Deadline <= 0 {
		return fmt.Errorf("duration-aware drains require a positive deadline")
	}
	if d.BackfillBuffer < 0 {
		return fmt.Errorf("backfill buffer must not be negative")
	}
	if !d.DurationAware && d.BackfillBuffer != 0 {
		return fmt.Errorf("backfill buffer requires a duration-aware drain")
	}
	return nil
}

func (d *DrainSpec) backfillBuffer() time.Duration {
	if d.BackfillBuffer == 0 {
		return DefaultBackfillBuffer
	}
	return d.BackfillBuffer
}

// BackfillAdmissionOpen is only a candidate-node check. Every placement must also
// pass CanBackfill, which checks the job and task group's time budget.
func (n *Node) BackfillAdmissionOpen(now time.Time) bool {
	if n == nil || n.Status != NodeStatusReady {
		return false
	}
	d := n.DrainStrategy
	return d != nil && d.DurationAware && !d.BackfillClosed && d.Deadline > 0 &&
		d.BackfillBuffer >= 0 && d.ForceDeadline.Sub(now) > d.backfillBuffer()
}

// BackfillShutdownAllowance conservatively accounts for sequential shutdown
// phases. Saturation prevents very large configured durations from wrapping.
func (tg *TaskGroup) BackfillShutdownAllowance() time.Duration {
	var total time.Duration
	add := func(d time.Duration) {
		if d < 0 || d > time.Duration(math.MaxInt64)-total {
			total = time.Duration(math.MaxInt64)
		} else {
			total += d
		}
	}
	if tg.ShutdownDelay != nil {
		add(*tg.ShutdownDelay)
	}
	for _, task := range tg.Tasks {
		add(task.ShutdownDelay)
		add(task.KillTimeout)
	}
	return total
}

func backfillBounded(job *Job, tg *TaskGroup) bool {
	return job != nil && tg != nil && !job.IsPlugin() &&
		(job.Type == JobTypeBatch || job.Type == JobTypeSysBatch) &&
		tg.MaxRunDuration != nil && *tg.MaxRunDuration > 0
}

// CanBackfill requires the complete runtime plus shutdown allowance and buffer
// to fit strictly before the absolute drain deadline.
func (n *Node) CanBackfill(job *Job, tg *TaskGroup, now time.Time) bool {
	if !n.BackfillAdmissionOpen(now) || !backfillBounded(job, tg) {
		return false
	}
	remaining := n.DrainStrategy.ForceDeadline.Sub(now) - n.DrainStrategy.backfillBuffer()
	shutdown := tg.BackfillShutdownAllowance()
	return shutdown < remaining && *tg.MaxRunDuration < remaining-shutdown
}

// BackfillUpdateAllowed preserves ordinary in-place updates and decreases in
// runtime, but prevents updates from extending work beyond the drain window.
// The original creation time, not the update time, anchors the runtime limit.
func (n *Node) BackfillUpdateAllowed(existing, updated *Allocation, now time.Time) bool {
	if updated.TerminalStatus() || existing.TerminalStatus() {
		return true
	}
	oldMax, oldBounded := existing.MaxRunDuration()
	if !oldBounded {
		return true // existing unbounded work already follows ordinary draining
	}
	newMax, newBounded := updated.MaxRunDuration()
	if !newBounded {
		return false
	}
	oldTG := existing.Job.LookupTaskGroup(existing.TaskGroup)
	newTG := updated.Job.LookupTaskGroup(updated.TaskGroup)
	if newMax <= oldMax && newTG.BackfillShutdownAllowance() <= oldTG.BackfillShutdownAllowance() {
		return true
	}
	if !n.BackfillAdmissionOpen(now) || existing.CreateTime == 0 || !backfillBounded(updated.Job, newTG) {
		return false
	}
	expires := time.Unix(0, existing.CreateTime).Add(newMax)
	if expires.Before(now) {
		expires = now
	}
	remaining := n.DrainStrategy.ForceDeadline.Sub(expires)
	buffer := n.DrainStrategy.backfillBuffer()
	return remaining > buffer && remaining-buffer > newTG.BackfillShutdownAllowance()
}
