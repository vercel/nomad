// Copyright IBM Corp. 2026
// SPDX-License-Identifier: BUSL-1.1

package client

import (
	"testing"
	"time"

	"github.com/hashicorp/nomad/ci"
	"github.com/hashicorp/nomad/nomad/mock"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/shoenig/test/must"
	"github.com/shoenig/test/wait"
)

// TestClient_ServicesAndChecksWithoutHooks exercises a single allocation with
// service discovery and health checks still present in the jobspec after their
// hooks have been disabled in Vercel's fork. Run the full lifecycle: constructing
// a runner alone would miss panics in asynchronous task and allocation hooks.
func TestClient_ServicesAndChecksWithoutHooks(t *testing.T) {
	ci.Parallel(t)

	client, cleanup := TestClient(t, nil)
	t.Cleanup(func() { must.NoError(t, cleanup()) })

	job := mock.MinJob()
	group := job.TaskGroups[0]
	task := group.Tasks[0]
	group.Networks = structs.Networks{{
		Mode:          "host",
		ReservedPorts: []structs.Port{{Label: "http", Value: 12345}},
	}}
	services := func(name string) []*structs.Service {
		return []*structs.Service{{
			Name:      name,
			Provider:  structs.ServiceProviderConsul,
			PortLabel: "http",
			TaskName:  task.Name,
			Checks: []*structs.ServiceCheck{
				{
					Name:     name + "-http",
					Type:     structs.ServiceCheckHTTP,
					Path:     "/health",
					Interval: time.Second,
					Timeout:  time.Second,
				},
				{
					Name:     name + "-tcp",
					Type:     structs.ServiceCheckTCP,
					Interval: time.Second,
					Timeout:  time.Second,
				},
				{
					Name:     name + "-script",
					Type:     structs.ServiceCheckScript,
					Command:  "true",
					Interval: time.Second,
					Timeout:  time.Second,
				},
			},
		}}
	}
	group.Services = services("group-service")
	task.Services = services("task-service")
	job.Canonicalize()

	alloc := mock.MinAllocForJob(job)
	alloc.NodeID = client.NodeID()
	alloc.AllocatedResources.Shared.Networks = group.Networks.Copy()
	alloc.AllocatedResources.Shared.Networks[0].IP = "127.0.0.1"
	alloc.AllocatedResources.Shared.Ports = structs.AllocatedPorts{{
		Label: "http", Value: 12345, HostIP: "127.0.0.1",
	}}
	must.NoError(t, client.addAlloc(alloc, ""))
	ar, err := client.getAllocRunner(alloc.ID)
	must.NoError(t, err)
	t.Cleanup(func() {
		ar.Destroy()
		select {
		case <-ar.DestroyCh():
		case <-time.After(10 * time.Second):
			t.Error("timed out destroying allocation with services and checks")
		}
	})

	must.Wait(t, wait.InitialSuccess(
		wait.BoolFunc(func() bool {
			state := ar.AllocState()
			return state.ClientStatus == structs.AllocClientStatusRunning &&
				state.TaskStates[task.Name] != nil &&
				state.TaskStates[task.Name].State == structs.TaskStateRunning
		}),
		wait.Timeout(10*time.Second),
		wait.Gap(10*time.Millisecond),
	))

	// Stopping also exercises update, deregistration, and script-check cleanup.
	stopped := alloc.Copy()
	stopped.AllocModifyIndex++
	stopped.DesiredStatus = structs.AllocDesiredStatusStop
	ar.Update(stopped)
	select {
	case <-ar.WaitCh():
	case <-time.After(10 * time.Second):
		t.Fatal("timed out stopping allocation with services and checks")
	}
	state := ar.AllocState()
	must.Eq(t, structs.AllocClientStatusComplete, state.ClientStatus)
	must.Eq(t, structs.TaskStateDead, state.TaskStates[task.Name].State)
	must.False(t, state.TaskStates[task.Name].Failed)
}
