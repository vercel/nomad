// Copyright IBM Corp. 2015, 2026
// SPDX-License-Identifier: BUSL-1.1

package ipaddr

import (
	"fmt"
	"strings"

	"github.com/hashicorp/go-secure-stdlib/listenerutil"
	"github.com/hashicorp/go-sockaddr/template"
)

// ParseTemplate renders a go-sockaddr template, preserving literal values without interface discovery.
func ParseTemplate(input string) (string, error) {
	if !strings.Contains(input, "{{") {
		return input, nil
	}
	return template.Parse(input)
}

// ParseSingleIPTemplate renders an address template and requires a single space-delimited result.
func ParseSingleIPTemplate(input string) (string, error) {
	if strings.Contains(input, "{{") {
		return listenerutil.ParseSingleIPTemplate(input)
	}
	addresses := strings.Split(input, " ")
	if len(addresses) != 1 {
		return "", fmt.Errorf("multiple addresses found (%q), please configure one", input)
	}
	return strings.TrimSpace(addresses[0]), nil
}
