// Copyright IBM Corp. 2015, 2025
// SPDX-License-Identifier: BUSL-1.1

package numalib

import (
	"testing"

	"github.com/shoenig/test/must"
)

// TestScanTopology is going to be different on every machine; even the CI
// systems change sometimes so it's hard to make good assertions here.
func TestScanTopology(t *testing.T) {
	top := Scan(PlatformScanners(false))
	must.Positive(t, top.UsableCompute())
	must.Positive(t, top.TotalCompute())
	must.Positive(t, top.NumCores())
}

func TestTopologyEqual_CoreCompute(t *testing.T) {
	top := &Topology{OverrideTotalCompute: 4000, OverrideCoreCompute: 1000}
	other := &Topology{OverrideTotalCompute: 4000, OverrideCoreCompute: 2000}

	must.False(t, top.Equal(other))
	other.OverrideCoreCompute = top.OverrideCoreCompute
	must.True(t, top.Equal(other))
}
