// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package nomad

import (
	"testing"
	"time"

	"github.com/hashicorp/nomad/nomad/mock"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/testutil"
	"github.com/shoenig/test/must"
)

func TestDrainer_BackfillEndToEnd(t *testing.T) {
	t.Parallel()
	srv, cleanup := TestServer(t, nil)
	defer cleanup()
	testutil.WaitForKeyring(t, srv.RPC, srv.Region())
	store := srv.State()
	node := mock.Node()
	testRegisterNode(t, srv, node)
	register := func(job *structs.Job) {
		job.TaskGroups[0].Count = 1
		if job.Type == structs.JobTypeSysBatch {
			job.TaskGroups[0].RestartPolicy = structs.NewRestartPolicy(structs.JobTypeBatch)
		}
		var resp structs.JobRegisterResponse
		must.NoError(t, srv.RPC("Job.Register", &structs.JobRegisterRequest{
			Job: job, WriteRequest: structs.WriteRequest{Region: "global", Namespace: job.Namespace},
		}, &resp))
	}
	long := mock.BatchJob()
	long.TaskGroups[0].MaxRunDuration = new(2 * time.Hour)
	register(long)
	waitForPlacedAllocs(t, store, node.ID, 1)
	drain := &structs.NodeUpdateDrainRequest{
		NodeID: node.ID, DrainStrategy: &structs.DrainStrategy{
			DrainSpec: structs.DrainSpec{Deadline: time.Hour, DurationAware: true},
		}, WriteRequest: structs.WriteRequest{Region: "global"},
	}
	var resp structs.NodeDrainUpdateResponse
	must.NoError(t, srv.RPC("Node.UpdateDrain", drain, &resp))
	for _, job := range []*structs.Job{mock.BatchJob(), mock.SystemBatchJob()} {
		job.TaskGroups[0].MaxRunDuration = new(time.Minute)
		register(job)
	}
	waitForPlacedAllocs(t, store, node.ID, 3)
	allocs, err := store.AllocsByNode(nil, node.ID)
	must.NoError(t, err)
	for _, alloc := range allocs {
		must.Eq(t, structs.AllocDesiredStatusRun, alloc.DesiredStatus)
		must.False(t, alloc.DesiredTransition.ShouldMigrate())
	}

	// Updating the absolute deadline closes admission and drains all jobs,
	// including the two first registered after the drain started.
	drain.DrainStrategy.Deadline = 50 * time.Millisecond
	must.NoError(t, srv.RPC("Node.UpdateDrain", drain, &resp))
	waitForAllocsStop(t, store, node.ID, nil)
	testutil.WaitForResult(func() (bool, error) {
		node, err := store.NodeByID(nil, node.ID)
		return err == nil && node.DrainStrategy == nil && node.SchedulingEligibility == structs.NodeSchedulingIneligible, err
	}, func(err error) { t.Fatal(err) })
}
