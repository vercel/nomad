// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package ipaddr

import (
	"testing"

	"github.com/hashicorp/go-secure-stdlib/listenerutil"
	"github.com/hashicorp/go-sockaddr/template"
	"github.com/hashicorp/nomad/ci"
	"github.com/shoenig/test/must"
)

func TestParseTemplate_Compatibility(t *testing.T) {
	ci.Parallel(t)

	inputs := []string{
		"",
		"0.0.0.0",
		"192.0.2.10",
		"::",
		"2001:db8::10",
		"[2001:db8::10]:8500",
		"localhost:8500",
		"http://localhost:8500",
		"unix:///run/consul.sock",
		"eth0",
		"Ethernet 2",
		" ",
		" 192.0.2.10",
		"192.0.2.10 ",
		"\t192.0.2.10\n",
		"192.0.2.10 192.0.2.11",
		"192.0.2.10  192.0.2.11",
		"192.0.2.10\t192.0.2.11",
		"{literal}",
		"}}",
		`{{ print "192.0.2.10" }}`,
		`{{ print "2001:db8::10" }}:8500`,
		`{{ print "" }}`,
		`{{ print " 192.0.2.10 " }}`,
		`{{ print "192.0.2.10 192.0.2.11" }}`,
		`{{ print "Ethernet 2" }}`,
		`{{ print "{{" }}`,
		`{{ unknownFunction }}`,
		`{{`,
		`{{ index "x" 2 }}`,
	}
	parsers := []struct {
		name     string
		parse    func(string) (string, error)
		upstream func(string) (string, error)
	}{
		{"template", ParseTemplate, template.Parse},
		{"single address", ParseSingleIPTemplate, listenerutil.ParseSingleIPTemplate},
	}
	for _, parser := range parsers {
		t.Run(parser.name, func(t *testing.T) {
			ci.Parallel(t)
			for _, input := range inputs {
				t.Run(input, func(t *testing.T) {
					ci.Parallel(t)
					want, wantErr := parser.upstream(input)
					got, gotErr := parser.parse(input)
					must.Eq(t, want, got)
					if wantErr != nil {
						must.Error(t, gotErr)
						must.Eq(t, wantErr.Error(), gotErr.Error())
					} else {
						must.NoError(t, gotErr)
					}
				})
			}
		})
	}
}

// Allocation accounting requires serial execution.
func TestParseTemplate_LiteralAllocations(t *testing.T) {
	for _, input := range []string{"", "0.0.0.0", "[2001:db8::10]:8500", "localhost:8500", "Ethernet 2"} {
		t.Run(input, func(t *testing.T) {
			allocs := testing.AllocsPerRun(100, func() {
				output, err := ParseTemplate(input)
				if err != nil || output != input {
					t.Fatalf("unexpected literal result: %q, %v", output, err)
				}
			})
			must.Eq(t, float64(0), allocs)
		})
	}
	for _, input := range []string{"", "0.0.0.0", "[2001:db8::10]:8500", "localhost:8500"} {
		t.Run("single address/"+input, func(t *testing.T) {
			allocs := testing.AllocsPerRun(100, func() {
				output, err := ParseSingleIPTemplate(input)
				if err != nil || output != input {
					t.Fatalf("unexpected literal result: %q, %v", output, err)
				}
			})
			must.True(t, allocs <= 1, must.Sprintf("allocations: %v", allocs))
		})
	}
}

func BenchmarkParseTemplate_Literal(b *testing.B) {
	for _, parser := range []struct {
		name  string
		parse func(string) (string, error)
	}{
		{"literal", ParseTemplate},
		{"sockaddr", template.Parse},
		{"single literal", ParseSingleIPTemplate},
		{"single sockaddr", listenerutil.ParseSingleIPTemplate},
	} {
		b.Run(parser.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				output, err := parser.parse("192.0.2.10")
				if err != nil || output != "192.0.2.10" {
					b.Fatalf("unexpected literal result: %q, %v", output, err)
				}
			}
		})
	}
}
