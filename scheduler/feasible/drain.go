// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package feasible

import (
	"time"

	"github.com/hashicorp/nomad/nomad/structs"
)

// DrainChecker is a transient check: neither drain state nor remaining time is
// a property of a computed node class.
type DrainChecker struct {
	ctx      Context
	job      *structs.Job
	tg       *structs.TaskGroup
	existing *structs.Allocation
}

func (c *DrainChecker) Feasible(node *structs.Node) bool {
	if node.DrainStrategy == nil {
		return true
	}
	if c.existing != nil && node.DrainStrategy.DurationAware {
		updated := *c.existing
		updated.Job = c.job
		if node.BackfillUpdateAllowed(c.existing, &updated, time.Now()) {
			return true
		}
	} else if node.CanBackfill(c.job, c.tg, time.Now()) {
		return true
	}
	c.ctx.Metrics().FilterNode(node, "node drain time budget")
	return false
}
