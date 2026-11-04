// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: BUSL-1.1

package fingerprint

import (
	"fmt"
	"net"
	"sort"
	"testing"

	"github.com/hashicorp/nomad/ci"
	"github.com/hashicorp/nomad/client/config"
	"github.com/hashicorp/nomad/helper/testlog"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/shoenig/test/must"
)

type selectionNetworkDetector struct {
	interfaces []net.Interface
	addrCalls  []string
}

func (d *selectionNetworkDetector) Interfaces() ([]net.Interface, error) {
	return d.interfaces, nil
}

func (d *selectionNetworkDetector) InterfaceByName(name string) (*net.Interface, error) {
	for i := range d.interfaces {
		if d.interfaces[i].Name == name {
			return &d.interfaces[i], nil
		}
	}
	return nil, fmt.Errorf("interface %q not found", name)
}

func (d *selectionNetworkDetector) Addrs(iface *net.Interface) ([]net.Addr, error) {
	d.addrCalls = append(d.addrCalls, iface.Name)
	switch iface.Name {
	case "eth0":
		return []net.Addr{&net.IPAddr{IP: net.ParseIP("192.0.2.1")}}, nil
	case "eth1":
		return []net.Addr{&net.IPAddr{IP: net.ParseIP("198.51.100.1")}}, nil
	default:
		return []net.Addr{&net.IPAddr{IP: net.ParseIP("203.0.113.1")}}, nil
	}
}

func TestNetworkFingerprint_InterfaceSelection(t *testing.T) {
	ci.Parallel(t)

	cases := []struct {
		name         string
		hostNetwork  *structs.ClientHostNetworkConfig
		wantScanned  []string
		wantNetworks map[string][]string
	}{
		{
			name:         "default interface only",
			wantScanned:  []string{"eth0"},
			wantNetworks: map[string][]string{"eth0": {"default"}},
		},
		{
			name:         "named virtual interface",
			hostNetwork:  &structs.ClientHostNetworkConfig{Interface: "veth0", ReservedPorts: "8080"},
			wantScanned:  []string{"eth0", "veth0"},
			wantNetworks: map[string][]string{"eth0": {"default"}, "veth0": {"custom"}},
		},
		{
			name:         "interface template",
			hostNetwork:  &structs.ClientHostNetworkConfig{Interface: `{{ print "veth0" }}`},
			wantScanned:  []string{"eth0", "veth0"},
			wantNetworks: map[string][]string{"eth0": {"default"}, "veth0": {"custom"}},
		},
		{
			name:         "invalid interface template",
			hostNetwork:  &structs.ClientHostNetworkConfig{Interface: "{{"},
			wantScanned:  []string{"eth0"},
			wantNetworks: map[string][]string{"eth0": {"default"}},
		},
		{
			name:         "empty template result",
			hostNetwork:  &structs.ClientHostNetworkConfig{Interface: `{{ print "" }}`},
			wantScanned:  []string{"eth0"},
			wantNetworks: map[string][]string{"eth0": {"default"}},
		},
		{
			name:         "CIDR only",
			hostNetwork:  &structs.ClientHostNetworkConfig{CIDR: "203.0.113.0/24"},
			wantScanned:  []string{"eth0", "eth1", "veth0"},
			wantNetworks: map[string][]string{"eth0": {"default"}, "veth0": {"custom"}},
		},
		{
			name:         "multiple CIDRs",
			hostNetwork:  &structs.ClientHostNetworkConfig{CIDR: "198.51.100.0/24,203.0.113.0/24"},
			wantScanned:  []string{"eth0", "eth1", "veth0"},
			wantNetworks: map[string][]string{"eth0": {"default"}, "eth1": {"custom"}, "veth0": {"custom"}},
		},
		{
			name:         "unrestricted host network",
			hostNetwork:  &structs.ClientHostNetworkConfig{},
			wantScanned:  []string{"eth0", "eth1", "veth0"},
			wantNetworks: map[string][]string{"eth0": {"custom", "default"}, "eth1": {"custom"}, "veth0": {"custom"}},
		},
		{
			name:         "interface and CIDR must both match",
			hostNetwork:  &structs.ClientHostNetworkConfig{Interface: "veth0", CIDR: "198.51.100.0/24"},
			wantScanned:  []string{"eth0", "veth0"},
			wantNetworks: map[string][]string{"eth0": {"default"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ci.Parallel(t)
			detector := &selectionNetworkDetector{
				interfaces: []net.Interface{eth0, eth1, {Name: "veth0"}},
			}
			f := &NetworkFingerprint{logger: testlog.HCLogger(t), interfaceDetector: detector}
			cfg := &config.Config{}
			var original structs.ClientHostNetworkConfig
			if tc.hostNetwork != nil {
				original = *tc.hostNetwork
				cfg.HostNetworks = map[string]*structs.ClientHostNetworkConfig{"custom": tc.hostNetwork}
			}

			networks, err := f.createNodeNetworkResources(detector.interfaces, false, cfg, "eth0")
			must.NoError(t, err)
			must.Eq(t, tc.wantScanned, detector.addrCalls)
			got := make(map[string][]string)
			for _, network := range networks {
				must.Positive(t, network.Speed)
				for _, address := range network.Addresses {
					got[network.Device] = append(got[network.Device], address.Alias)
					if address.Alias == "custom" {
						must.Eq(t, tc.hostNetwork.ReservedPorts, address.ReservedPorts)
					}
				}
				sort.Strings(got[network.Device])
			}
			must.Eq(t, tc.wantNetworks, got)
			must.Eq(t, "", cfg.NetworkInterface)
			if tc.hostNetwork != nil {
				must.Eq(t, original, *tc.hostNetwork)
			}
		})
	}
}

func TestNetworkFingerprint_ManyUnselectedInterfaces(t *testing.T) {
	ci.Parallel(t)

	detector := &selectionNetworkDetector{interfaces: []net.Interface{eth0}}
	for i := 0; i < 1024; i++ {
		detector.interfaces = append(detector.interfaces, net.Interface{Name: fmt.Sprintf("veth%d", i)})
	}
	f := &NetworkFingerprint{logger: testlog.HCLogger(t), interfaceDetector: detector}
	cfg := &config.Config{NetworkInterface: "eth0"}
	var response FingerprintResponse
	must.NoError(t, f.Fingerprint(&FingerprintRequest{Config: cfg}, &response))
	must.True(t, response.Detected)
	must.Eq(t, []string{"eth0", "eth0"}, detector.addrCalls)
	must.Len(t, 1, response.NodeResources.NodeNetworks)
	must.Eq(t, "eth0", response.NodeResources.NodeNetworks[0].Device)
}

func TestNetworkFingerprint_SelectedVirtualInterface(t *testing.T) {
	ci.Parallel(t)

	detector := &selectionNetworkDetector{interfaces: []net.Interface{eth0, {Name: "veth0"}}}
	f := &NetworkFingerprint{logger: testlog.HCLogger(t), interfaceDetector: detector}
	cfg := &config.Config{NetworkInterface: "veth0"}
	var response FingerprintResponse
	must.NoError(t, f.Fingerprint(&FingerprintRequest{Config: cfg}, &response))
	must.Eq(t, []string{"veth0", "veth0"}, detector.addrCalls)
	must.Len(t, 1, response.NodeResources.NodeNetworks)
	network := response.NodeResources.NodeNetworks[0]
	must.Eq(t, "veth0", network.Device)
	must.Len(t, 1, network.Addresses)
	must.Eq(t, "default", network.Addresses[0].Alias)
}
