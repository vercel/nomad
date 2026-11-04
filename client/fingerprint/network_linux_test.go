// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: BUSL-1.1

package fingerprint

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/nomad/client/config"
	"github.com/hashicorp/nomad/helper/testlog"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/stretchr/testify/require"
)

func TestNetworkFingerprint_LinuxScanCommands(t *testing.T) {
	// PATH changes require serial execution.
	tests := []struct {
		name         string
		hostNetworks map[string]*structs.ClientHostNetworkConfig
		scanAll      bool
		scanned      []string
		emitAlias    bool
	}{
		{
			name:    "default interface only",
			scanned: []string{"eth0"},
		},
		{
			name: "named interfaces without advertised addresses skip speed detection",
			hostNetworks: map[string]*structs.ClientHostNetworkConfig{
				"private":  {Interface: "eth1", CIDR: "198.51.100.0/24", ReservedPorts: "8080"},
				"empty":    {Interface: "empty0"},
				"local":    {Interface: "local0"},
				"filtered": {Interface: "filtered0", CIDR: "198.51.100.0/24"},
			},
			scanned:   []string{"eth0", "eth1", "empty0", "local0", "filtered0"},
			emitAlias: true,
		},
		{
			name: "CIDR-only alias scans every interface but only probes advertised interfaces",
			hostNetworks: map[string]*structs.ClientHostNetworkConfig{
				"private": {CIDR: "198.51.100.0/24", ReservedPorts: "8080"},
			},
			scanAll:   true,
			emitAlias: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ipLog, ethtoolLog := networkLinuxScanCommands(t)
			detector := newNetworkLinuxScanDetector()
			f := &NetworkFingerprint{
				logger:            testlog.HCLogger(t),
				interfaceDetector: detector,
			}
			cfg := &config.Config{
				HostNetworks: tc.hostNetworks,
				Options: map[string]string{
					networkDisallowLinkLocalOption: "true",
				},
			}
			var resp FingerprintResponse
			require.NoError(t, f.Fingerprint(&FingerprintRequest{Config: cfg}, &resp))
			require.True(t, resp.Detected)
			require.Equal(t, []string{"route"}, networkLinuxScanCommandCalls(t, ipLog),
				"default route lookup must not repeat for each interface or address")

			// The selected interface supplies legacy and current network resources.
			wantProbes := []string{"eth0", "eth0"}
			if tc.emitAlias {
				wantProbes = append(wantProbes, "eth1")
			}
			require.ElementsMatch(t, wantProbes, networkLinuxScanCommandCalls(t, ethtoolLog))

			wantScans := map[string]int{"eth0": 2}
			if tc.scanAll {
				for _, iface := range detector.interfaces {
					if iface.Name != "eth0" {
						wantScans[iface.Name] = 1
					}
				}
			} else {
				for _, name := range tc.scanned {
					if name != "eth0" {
						wantScans[name] = 1
					}
				}
			}
			require.Equal(t, wantScans, detector.addrCalls)

			require.Len(t, resp.NodeResources.Networks, 2)
			for _, network := range resp.NodeResources.Networks {
				require.Equal(t, "eth0", network.Device)
				require.Equal(t, 2500, network.MBits)
			}
			wantAddresses := map[string][]structs.NodeNetworkAddress{
				"eth0": {
					{Address: "192.0.2.10", Family: structs.NodeNetworkAF_IPv4, Alias: "default"},
					{Address: "2001:db8::10", Family: structs.NodeNetworkAF_IPv6, Alias: "default"},
				},
			}
			if tc.emitAlias {
				wantAddresses["eth1"] = []structs.NodeNetworkAddress{
					{Address: "198.51.100.20", Family: structs.NodeNetworkAF_IPv4, Alias: "private", ReservedPorts: "8080"},
				}
			}
			require.Len(t, resp.NodeResources.NodeNetworks, len(wantAddresses))
			for _, network := range resp.NodeResources.NodeNetworks {
				require.Equal(t, "host", network.Mode)
				require.Equal(t, 2500, network.Speed)
				require.ElementsMatch(t, wantAddresses[network.Device], network.Addresses)
			}
		})
	}
}

func networkLinuxScanCommands(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	ipLog := filepath.Join(dir, "ip.log")
	ethtoolLog := filepath.Join(dir, "ethtool.log")
	for _, path := range []string{ipLog, ethtoolLog} {
		require.NoError(t, os.WriteFile(path, nil, 0600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ip"), []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$NOMAD_NETWORK_SCAN_IP_LOG"
printf '%s\n' 'default via 192.0.2.1 dev eth0'
`), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ethtool"), []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$NOMAD_NETWORK_SCAN_ETHTOOL_LOG"
printf '%s\n' 'Speed: 2500Mb/s'
`), 0700))
	t.Setenv("NOMAD_NETWORK_SCAN_IP_LOG", ipLog)
	t.Setenv("NOMAD_NETWORK_SCAN_ETHTOOL_LOG", ethtoolLog)
	t.Setenv("PATH", dir)
	return ipLog, ethtoolLog
}

func networkLinuxScanCommandCalls(t *testing.T, path string) []string {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.Fields(string(contents))
}

type networkLinuxScanDetector struct {
	interfaces []net.Interface
	addresses  map[string][]net.Addr
	addrCalls  map[string]int
}

func newNetworkLinuxScanDetector() *networkLinuxScanDetector {
	d := &networkLinuxScanDetector{
		addresses: make(map[string][]net.Addr),
		addrCalls: make(map[string]int),
	}
	add := func(name string, addresses ...string) {
		d.interfaces = append(d.interfaces, net.Interface{Name: name})
		for _, address := range addresses {
			d.addresses[name] = append(d.addresses[name], &net.IPAddr{IP: net.ParseIP(address)})
		}
	}
	add("eth0", "192.0.2.10", "2001:db8::10", "fe80::10")
	add("eth1", "198.51.100.20", "203.0.113.20")
	add("empty0")
	add("local0", "169.254.1.1", "fe80::1")
	add("filtered0", "203.0.113.30")
	for i := 0; i < 64; i++ {
		add(fmt.Sprintf("veth%d", i), fmt.Sprintf("10.0.%d.1", i),
			fmt.Sprintf("2001:db8:1::%x", i+1), fmt.Sprintf("fe80::%x", i+1))
	}
	return d
}

func (d *networkLinuxScanDetector) Interfaces() ([]net.Interface, error) {
	return d.interfaces, nil
}

func (d *networkLinuxScanDetector) InterfaceByName(name string) (*net.Interface, error) {
	for _, iface := range d.interfaces {
		if iface.Name == name {
			return &iface, nil
		}
	}
	return nil, fmt.Errorf("unknown interface %q", name)
}

func (d *networkLinuxScanDetector) Addrs(iface *net.Interface) ([]net.Addr, error) {
	d.addrCalls[iface.Name]++
	return d.addresses[iface.Name], nil
}
