// See LICENSE file in the project root for license information.

package rstream

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"testing/iotest"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/quicvarint"
)

func TestDiscardMASQUECapsulesConsumesFragmentedUnknownCapsules(t *testing.T) {
	var wire bytes.Buffer
	for i, body := range [][]byte{nil, []byte("unknown control capsule"), bytes.Repeat([]byte{0x5a}, 65537), nil} {
		if err := http3.WriteCapsule(quicvarint.NewWriter(&wire), http3.CapsuleType(uint64(i)<<40), body); err != nil {
			t.Fatal(err)
		}
	}
	if err := discardMASQUECapsules(iotest.OneByteReader(&wire)); !errors.Is(err, io.EOF) {
		t.Fatalf("discard capsules: %v, want clean EOF", err)
	}
}

func TestDiscardMASQUECapsulesRejectsTruncation(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire []byte
	}{
		{name: "type", wire: []byte{0x40}},
		{name: "missing length", wire: []byte{1}},
		{name: "length", wire: []byte{1, 0x40}},
		{name: "missing body", wire: []byte{1, 1}},
		{name: "partial body", wire: []byte{1, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := discardMASQUECapsules(iotest.OneByteReader(bytes.NewReader(tc.wire))); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("discard truncated capsule: %v, want unexpected EOF", err)
			}
		})
	}
}

func TestDiscardMASQUECapsulesPreservesReadError(t *testing.T) {
	if err := discardMASQUECapsules(iotest.ErrReader(io.ErrClosedPipe)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("discard capsules: %v, want closed pipe", err)
	}
}
