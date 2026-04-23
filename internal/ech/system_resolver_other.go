// See LICENSE file in the project root for license information.

//go:build !darwin

package ech

import (
	"context"

	"github.com/miekg/dns"
)

func systemNameservers(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func fallbackNameservers(_ context.Context, _ string) ([]string, error) {
	return readResolvConfNameservers()
}

func readResolvConfNameservers() ([]string, error) {
	config, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		return nil, err
	}
	if len(config.Servers) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(config.Servers))
	for _, server := range config.Servers {
		out = append(out, ensurePort(server, config.Port))
	}
	return out, nil
}
