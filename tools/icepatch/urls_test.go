package main

import (
	"testing"

	"github.com/pion/ice/v4"
	"github.com/pion/transport/v4/test"
)

func TestURLUpdateDuringGathering(t *testing.T) {
	defer test.CheckRoutines(t)()
	for _, candidateType := range []ice.CandidateType{ice.CandidateTypeRelay, ice.CandidateTypeServerReflexive} {
		t.Run(candidateType.String(), func(t *testing.T) {
			agent, err := ice.NewAgentWithOptions(
				ice.WithCandidateTypes([]ice.CandidateType{candidateType}),
				ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4}),
				ice.WithMulticastDNSMode(ice.MulticastDNSModeDisabled),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := agent.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := agent.OnCandidate(func(ice.Candidate) {}); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				for range 1024 {
					if err := agent.UpdateOptions(ice.WithUrls(nil)); err != nil {
						done <- err
						return
					}
				}
				done <- nil
			}()
			for range 128 {
				if err := agent.Restart("", ""); err != nil {
					t.Fatal(err)
				}
				if err := agent.GatherCandidates(); err != nil {
					t.Fatal(err)
				}
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
