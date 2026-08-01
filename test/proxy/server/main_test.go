// See LICENSE file in the project root for license information.

package main

import (
	"errors"
	"testing"

	"github.com/quic-go/quic-go"
)

type stubMASQUEDatagramSender struct {
	err error
}

func (s *stubMASQUEDatagramSender) SendDatagram([]byte) error {
	return s.err
}

func TestSendMASQUEDatagram(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		dropped, err := sendMASQUEDatagram(&stubMASQUEDatagramSender{}, []byte("payload"))
		if err != nil || dropped {
			t.Fatalf("unexpected result: dropped=%t err=%v", dropped, err)
		}
	})
	t.Run("oversized datagram", func(t *testing.T) {
		sender := &stubMASQUEDatagramSender{err: &quic.DatagramTooLargeError{MaxDatagramPayloadSize: 1200}}
		dropped, err := sendMASQUEDatagram(sender, []byte("payload"))
		if err != nil || !dropped {
			t.Fatalf("unexpected result: dropped=%t err=%v", dropped, err)
		}
	})
	t.Run("transport error", func(t *testing.T) {
		expected := errors.New("transport failed")
		dropped, err := sendMASQUEDatagram(&stubMASQUEDatagramSender{err: expected}, []byte("payload"))
		if !errors.Is(err, expected) || dropped {
			t.Fatalf("unexpected result: dropped=%t err=%v", dropped, err)
		}
	})
}
