// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package state

import (
	"testing"
	"time"

	"github.com/hashicorp/nomad/helper/uuid"
	"github.com/hashicorp/nomad/nomad/mock"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/shoenig/test/must"
)

func TestStateStore_DrainBackfillFence(t *testing.T) {
	t.Parallel()
	s := TestStateStore(t)
	now := time.Now()
	node := mock.Node()
	node.SchedulingEligibility = structs.NodeSchedulingIneligible
	node.DrainStrategy = &structs.DrainStrategy{
		DrainSpec: structs.DrainSpec{Deadline: time.Hour, DurationAware: true}, ForceDeadline: now.Add(time.Hour),
	}
	must.NoError(t, s.UpsertNode(structs.MsgTypeTestSetup, 100, node))
	alloc := mock.BatchAlloc()
	alloc.NodeID = node.ID
	alloc.CreateTime = now.UnixNano()
	alloc.Job.TaskGroups[0].MaxRunDuration = new(time.Minute)
	must.NoError(t, s.UpsertJob(structs.MsgTypeTestSetup, 101, nil, alloc.Job))
	req := &structs.ApplyPlanResultsRequest{
		Job: alloc.Job, AllocsUpdated: []*structs.Allocation{alloc}, UpdatedAt: now.UnixNano(),
		BackfillNodeIndexes: map[string]uint64{node.ID: 100},
	}
	must.NoError(t, s.UpsertPlanResults(structs.MsgTypeTestSetup, 102, req))

	// Prepare a plan before closure, then commit it after closure.
	late := alloc.Copy()
	late.ID = uuid.Generate()
	req.AllocsUpdated = []*structs.Allocation{late}
	must.NoError(t, s.BatchUpdateNodeDrain(structs.MsgTypeTestSetup, 103, now.Unix(),
		map[string]*structs.DrainUpdate{node.ID: {CloseBackfill: true}}, nil))
	closed, err := s.NodeByID(nil, node.ID)
	must.NoError(t, err)
	must.True(t, closed.DrainStrategy.BackfillClosed)
	must.Eq(t, uint64(103), closed.ModifyIndex)
	must.Error(t, s.UpsertPlanResults(structs.MsgTypeTestSetup, 104, req))
	missing, err := s.AllocByID(nil, late.ID)
	must.NoError(t, err)
	must.Nil(t, missing)

	// A fresh fence cannot bypass closed admission either.
	req.BackfillNodeIndexes[node.ID] = closed.ModifyIndex
	must.Error(t, s.UpsertPlanResults(structs.MsgTypeTestSetup, 105, req))

	// Completing a drain remains a fence, even though its strategy is removed.
	must.NoError(t, s.BatchUpdateNodeDrain(structs.MsgTypeTestSetup, 106, now.Unix(),
		map[string]*structs.DrainUpdate{node.ID: {}}, nil))
	must.Error(t, s.UpsertPlanResults(structs.MsgTypeTestSetup, 107, req))
	req.BackfillNodeIndexes = nil // a plan made before draining began
	must.Error(t, s.UpsertPlanResults(structs.MsgTypeTestSetup, 108, req))

	// Explicitly cancelling and marking eligible permits ordinary scheduling.
	must.NoError(t, s.UpdateNodeDrain(structs.MsgTypeTestSetup, 109, node.ID, nil, true, now.Unix(), nil, nil, ""))
	must.NoError(t, s.UpsertPlanResults(structs.MsgTypeTestSetup, 110, req))
}

func TestStateStore_DrainBackfillRevalidation(t *testing.T) {
	t.Parallel()
	s := TestStateStore(t)
	now := time.Now()
	node := mock.Node()
	node.SchedulingEligibility = structs.NodeSchedulingIneligible
	node.DrainStrategy = &structs.DrainStrategy{
		DrainSpec: structs.DrainSpec{Deadline: time.Hour, DurationAware: true}, ForceDeadline: now.Add(time.Hour),
	}
	must.NoError(t, s.UpsertNode(structs.MsgTypeTestSetup, 100, node))
	alloc := mock.BatchAlloc()
	alloc.NodeID = node.ID
	alloc.CreateTime = now.UnixNano()
	alloc.Job.TaskGroups[0].MaxRunDuration = new(time.Minute)
	must.NoError(t, s.UpsertJob(structs.MsgTypeTestSetup, 101, nil, alloc.Job))
	req := &structs.ApplyPlanResultsRequest{
		Job: alloc.Job, AllocsUpdated: []*structs.Allocation{alloc}, UpdatedAt: now.UnixNano(),
	}
	must.Error(t, s.UpsertPlanResults(structs.MsgTypeTestSetup, 102, req)) // missing fence
	req.BackfillNodeIndexes = map[string]uint64{node.ID: 100}
	must.NoError(t, s.UpsertPlanResults(structs.MsgTypeTestSetup, 103, req))
	updated := alloc.Copy()
	updated.Job.TaskGroups[0].MaxRunDuration = new(2 * time.Hour)
	req.AllocsUpdated = []*structs.Allocation{updated}
	must.Error(t, s.UpsertPlanResults(structs.MsgTypeTestSetup, 104, req))
	updated.Job.TaskGroups[0].MaxRunDuration = nil
	must.Error(t, s.UpsertPlanResults(structs.MsgTypeTestSetup, 105, req))
	stored, err := s.AllocByID(nil, alloc.ID)
	must.NoError(t, err)
	duration, ok := stored.MaxRunDuration()
	must.True(t, ok)
	must.Eq(t, time.Minute, duration)
}
