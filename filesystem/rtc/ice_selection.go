// See LICENSE file in the project root for license information.

package rtc

import (
	"context"

	"github.com/pion/webrtc/v4"
)

type iceSelection struct {
	current func() bool
	changed chan struct{}
}

func observeICESelection(peer *webrtc.PeerConnection) *iceSelection {
	transport := peer.SCTP().Transport().ICETransport()
	selection := &iceSelection{changed: make(chan struct{}, 1), current: func() bool {
		_, ok := transport.GetSelectedCandidatePairStats()
		return ok
	}}
	transport.OnSelectedCandidatePairChange(func(*webrtc.ICECandidatePair) {
		select {
		case selection.changed <- struct{}{}:
		default:
		}
	})
	return selection
}

// Pion's local ICE restart clears selection synchronously. Recheck the current
// selection after installing its answer: stale/coalesced callbacks cannot prove
// a new path, and an already selected path must not wait for another callback.
func (s *iceSelection) wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.current() {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.changed:
		}
	}
}
