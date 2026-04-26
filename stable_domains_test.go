// See LICENSE file in the project root for license information.

package rstream

import (
	"strings"
	"testing"
)

func TestGenerateStableDomainFromProjectEngine(t *testing.T) {
	got, ok, err := GenerateStableDomain("f587ee53.c.localhost.rstream.io:443")
	if err != nil {
		t.Fatalf("GenerateStableDomain failed: %v", err)
	}
	if !ok {
		t.Fatal("expected stable domain")
	}
	if !strings.HasSuffix(got, "-f587ee53.t.c.localhost.rstream.io") {
		t.Fatalf("unexpected stable domain: %s", got)
	}
	label := strings.Split(got, ".")[0]
	if len(label) > 63 {
		t.Fatalf("label too long: %d", len(label))
	}
}

func TestGenerateStableDomainSkipsBaseEngine(t *testing.T) {
	if got, ok, err := GenerateStableDomain("localhost:443"); err != nil || ok || got != "" {
		t.Fatalf("GenerateStableDomain = %q, %t, %v; want empty false nil", got, ok, err)
	}
}

func TestMaybeSetGeneratedStableDomainSkipsPrivateTunnel(t *testing.T) {
	publish := false
	props := TunnelProperties{Publish: &publish}
	if err := MaybeSetGeneratedStableDomain(&props, "f587ee53.c.localhost.rstream.io:443"); err != nil {
		t.Fatalf("MaybeSetGeneratedStableDomain failed: %v", err)
	}
	if props.Hostname != nil {
		t.Fatalf("expected no hostname, got %q", *props.Hostname)
	}
}
