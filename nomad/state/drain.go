// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package state

import (
	"fmt"
	"time"

	"github.com/hashicorp/nomad/nomad/structs"
)

// validatePlanBackfill runs in the allocation write transaction. Use the leader's
// replicated timestamp, never a follower's wall clock, for deterministic replay.
func validatePlanBackfill(txn *txn, results *structs.ApplyPlanResultsRequest) error {
	for id, index := range results.BackfillNodeIndexes {
		raw, err := txn.First("nodes", "id", id)
		if err != nil {
			return err
		}
		if raw == nil || raw.(*structs.Node).ModifyIndex != index {
			return fmt.Errorf("backfill node %q changed while applying plan", id)
		}
	}
	for _, alloc := range results.AllocsUpdated {
		if alloc.TerminalStatus() {
			continue
		}
		raw, err := txn.First("nodes", "id", alloc.NodeID)
		if err != nil {
			return err
		}
		if raw == nil {
			continue
		}
		node := raw.(*structs.Node)
		durationAware := node.DrainStrategy != nil && node.DrainStrategy.DurationAware
		if !durationAware && node.Ready() {
			continue
		}
		rawExisting, err := txn.First("allocs", "id", alloc.ID)
		if err != nil {
			return err
		}
		if !durationAware {
			// Also reject pre-drain plans that arrive after the drain completed.
			if rawExisting == nil && !node.Ready() {
				return fmt.Errorf("node %q is no longer eligible for placement", node.ID)
			}
			continue
		}
		if _, fenced := results.BackfillNodeIndexes[node.ID]; !fenced {
			return fmt.Errorf("backfill node %q requires a fresh plan", node.ID)
		}
		updated := *alloc
		if updated.Job == nil {
			updated.Job = results.Job
		}
		now := time.Unix(0, results.UpdatedAt)
		if rawExisting != nil {
			if !node.BackfillUpdateAllowed(rawExisting.(*structs.Allocation), &updated, now) {
				return fmt.Errorf("allocation %q update exceeds node drain time budget", alloc.ID)
			}
		} else if updated.Job == nil || !node.CanBackfill(updated.Job, updated.Job.LookupTaskGroup(updated.TaskGroup), now) {
			return fmt.Errorf("allocation %q exceeds node drain time budget", alloc.ID)
		}
	}
	return nil
}

func closeNodeBackfill(txn *txn, index uint64, nodeID string) error {
	raw, err := txn.First("nodes", "id", nodeID)
	if err != nil || raw == nil {
		return err
	}
	node := raw.(*structs.Node)
	if node.DrainStrategy == nil || !node.DrainStrategy.DurationAware || node.DrainStrategy.BackfillClosed {
		return nil
	}
	updated := node.Copy()
	updated.DrainStrategy.BackfillClosed = true
	updated.ModifyIndex = index
	if err := txn.Insert("nodes", updated); err != nil {
		return err
	}
	return txn.Insert("index", &IndexEntry{"nodes", index})
}
