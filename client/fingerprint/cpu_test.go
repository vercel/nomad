// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package fingerprint

import (
	"runtime"
	"strconv"
	"testing"

	"github.com/hashicorp/nomad/ci"
	"github.com/hashicorp/nomad/client/config"
	"github.com/hashicorp/nomad/client/lib/numalib/hw"
	"github.com/hashicorp/nomad/helper/testlog"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/shoenig/test/must"
)

func TestCPUFingerprint_HackedCoreSpeeds(t *testing.T) {
	ci.Parallel(t)

	const millicore = 2_000

	f := NewCPUFingerprint(testlog.HCLogger(t))
	request := &FingerprintRequest{
		Config: config.DefaultConfig(),
		Node:   &structs.Node{Attributes: make(map[string]string)},
	}
	var response FingerprintResponse
	must.NoError(t, f.Fingerprint(request, &response))

	top := response.NodeResources.Processors.Topology
	nproc := top.NumCores()
	must.Positive(t, top.NumCores())
	must.Eq(t, hw.MHz(millicore*nproc), top.UsableCompute())

	expectedMHz := hw.MHz(millicore)
	for _, core := range top.Cores {
		must.Eq(t, expectedMHz, core.MHz())
		must.Eq(t, expectedMHz, core.BaseSpeed)
		must.Eq(t, expectedMHz, core.MaxSpeed)
		must.Eq(t, expectedMHz, core.GuessSpeed)
	}
}

func TestCPUFingerprint_OverrideCoreSpeeds(t *testing.T) {
	ci.Parallel(t)

	//HACK(schmichael) set total compute based on cores just to make this
	//work until the 2000 cpu per core hack is removed
	numCores := runtime.NumCPU()
	totalCompute := 2000 * numCores

	f := NewCPUFingerprint(testlog.HCLogger(t))
	request := &FingerprintRequest{
		Config: &config.Config{CpuCompute: totalCompute},
		Node:   &structs.Node{Attributes: make(map[string]string)},
	}
	var response FingerprintResponse
	must.NoError(t, f.Fingerprint(request, &response))

	top := response.NodeResources.Processors.Topology
	must.Positive(t, top.NumCores())
	must.Eq(t, hw.MHz(totalCompute), top.UsableCompute())

	expectedMHz := hw.MHz(totalCompute) / hw.MHz(top.NumCores())
	for _, core := range top.Cores {
		must.Eq(t, expectedMHz, core.MHz())
		must.Eq(t, expectedMHz, core.BaseSpeed)
		must.Eq(t, expectedMHz, core.MaxSpeed)
		must.Eq(t, expectedMHz, core.GuessSpeed)
	}
	must.Eq(t, strconv.Itoa(totalCompute), response.Attributes["cpu.totalcompute"])
	must.Eq(t, strconv.Itoa(totalCompute), response.Attributes["cpu.usablecompute"])
	must.Eq(t, int64(totalCompute), response.NodeResources.Cpu.CpuShares)

	performance, efficiency := top.CoreSpeeds()
	if efficiency > 0 {
		must.Eq(t, strconv.FormatUint(uint64(performance), 10), response.Attributes["cpu.frequency.performance"])
		must.Eq(t, strconv.FormatUint(uint64(efficiency), 10), response.Attributes["cpu.frequency.efficiency"])
	} else {
		must.Eq(t, strconv.FormatUint(uint64(performance), 10), response.Attributes["cpu.frequency"])
	}
}
