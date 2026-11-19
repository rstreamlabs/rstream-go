// See LICENSE file in the project root for license information.

package main

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type replyStream struct {
	reply    []byte
	fragment int
	finalEOF bool
	short    bool
	written  bytes.Buffer
}

func (s *replyStream) Read(p []byte) (int, error) {
	if len(s.reply) == 0 {
		return 0, io.EOF
	}
	if s.fragment > 0 && len(p) > s.fragment {
		p = p[:s.fragment]
	}
	n := copy(p, s.reply)
	s.reply = s.reply[n:]
	if s.finalEOF && len(s.reply) == 0 {
		return n, io.EOF
	}
	return n, nil
}

func (s *replyStream) Write(p []byte) (int, error) {
	if s.short {
		return s.written.Write(p[:len(p)-1])
	}
	return s.written.Write(p)
}

func TestExchangeStream(t *testing.T) {
	message := []byte("ping")
	for _, tc := range []struct {
		name     string
		reply    []byte
		fragment int
		finalEOF bool
		short    bool
		want     error
		mismatch bool
	}{
		{name: "fragmented", reply: message, fragment: 1},
		{name: "final bytes with EOF", reply: message, finalEOF: true},
		{name: "short write", short: true, want: io.ErrShortWrite},
		{name: "truncated reply", reply: message[:2], want: io.ErrUnexpectedEOF},
		{name: "incorrect reply", reply: bytes.Repeat([]byte("x"), len(message)), mismatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := &replyStream{reply: tc.reply, fragment: tc.fragment, finalEOF: tc.finalEOF, short: tc.short}
			err := exchangeStream(stream)
			if tc.mismatch {
				if err == nil {
					t.Fatal("accepted an incorrect echo")
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if err == nil && (!bytes.Equal(stream.written.Bytes(), message) || len(stream.reply) != 0) {
				t.Fatalf("incomplete exchange: written=%q unread=%d", stream.written.Bytes(), len(stream.reply))
			}
		})
	}
}
