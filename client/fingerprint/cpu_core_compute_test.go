// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package fingerprint

import (
	"testing"

	"github.com/hashicorp/nomad/client/lib/numalib"
	"github.com/hashicorp/nomad/helper/testlog"
	"github.com/shoenig/test/must"
)

func TestCPUFingerprint_CoreComputeAttribute(t *testing.T) {
	f := &CPUFingerprint{logger: testlog.HCLogger(t)}
	for _, tc := range []struct {
		name string
		top  numalib.Topology
		want string
	}{
		{"unset", numalib.Topology{OverrideTotalCompute: 16000}, ""},
		{"no total override", numalib.Topology{OverrideCoreCompute: 2000}, ""},
		{"configured", numalib.Topology{OverrideTotalCompute: 16000, OverrideCoreCompute: 2000}, "2000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response FingerprintResponse
			f.setTotalCompute(&response, &tc.top)
			must.Eq(t, tc.want, response.Attributes["cpu.corecompute"])
		})
	}
}
