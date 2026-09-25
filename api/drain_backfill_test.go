// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shoenig/test/must"
)

func TestNodes_MonitorDrain_BackfillEmptyInterval(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	empty, running, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		w.Header().Set("X-Nomad-Index", strconv.Itoa(int(request)))
		allocs := []*Allocation{}
		switch request {
		case 1:
			close(empty)
		case 2:
			allocs = append(allocs, &Allocation{ID: "backfill", ClientStatus: AllocClientStatusRunning, Job: &Job{Type: new(JobTypeBatch)}})
		default:
			if request == 3 {
				close(running)
			}
			select {
			case <-finish:
			case <-r.Context().Done():
				return
			}
			allocs = append(allocs, &Allocation{ID: "backfill", ClientStatus: AllocClientStatusComplete, Job: &Job{Type: new(JobTypeBatch)}})
		}
		_ = json.NewEncoder(w).Encode(allocs)
	}))
	defer server.Close()
	defer cancel()
	client, err := NewClient(&Config{Address: server.URL})
	must.NoError(t, err)
	done := make(chan struct{})
	out := make(chan *MonitorMessage, 8)
	go client.Nodes().monitorDrainAllocs(ctx, "node", false, out, done)
	select {
	case <-empty:
	case <-time.After(5 * time.Second):
		t.Fatal("no initial allocation query")
	}
	// Closing admission after an empty response must cause a fresh read that
	// observes the late allocation, rather than an early 'all stopped' result.
	close(done)
	select {
	case <-running:
	case <-time.After(5 * time.Second):
		t.Fatal("late backfill was not monitored")
	}
	select {
	case msg := <-out:
		t.Fatalf("monitor ended before backfill completed: %v", msg)
	default:
	}
	close(finish)
	var last *MonitorMessage
	for msg := range out {
		last = msg
	}
	must.NotNil(t, last)
	must.StrContains(t, last.Message, "All allocations")
}

func TestNodes_DrainStrategy_BackfillEqual(t *testing.T) {
	t.Parallel()
	base := &DrainStrategy{DrainSpec: DrainSpec{Deadline: time.Hour}}
	for _, change := range []func(*DrainStrategy){
		func(d *DrainStrategy) { d.DurationAware = true },
		func(d *DrainStrategy) { d.BackfillBuffer = time.Minute },
		func(d *DrainStrategy) { d.BackfillClosed = true },
	} {
		updated := *base
		change(&updated)
		must.False(t, base.Equal(&updated))
	}
}
