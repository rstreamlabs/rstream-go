// See LICENSE file in the project root for license information.

package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type echoStreamFixture struct {
	input      []byte
	output     bytes.Buffer
	readErr    error
	writeErr   error
	writeLimit int
	offset     int
	closed     bool
}

func (s *echoStreamFixture) Read(p []byte) (int, error) {
	if s.offset == len(s.input) {
		if s.readErr != nil {
			return 0, s.readErr
		}
		return 0, io.EOF
	}
	n := copy(p, s.input[s.offset:])
	s.offset += n
	if s.offset == len(s.input) {
		return n, s.readErr
	}
	return n, nil
}

func (s *echoStreamFixture) Write(p []byte) (int, error) {
	if s.writeErr != nil {
		return 0, s.writeErr
	}
	if s.writeLimit >= 0 && len(p) > s.writeLimit {
		return s.output.Write(p[:s.writeLimit])
	}
	return s.output.Write(p)
}

func (s *echoStreamFixture) Close() error {
	s.closed = true
	return nil
}

func TestEchoStream(t *testing.T) {
	failure := errors.New("stream failed")
	large := strings.Repeat("message", 8193)
	for _, test := range []struct {
		name       string
		input      string
		readErr    error
		writeErr   error
		writeLimit int
		want       string
		wantErr    error
	}{
		{name: "data with EOF", input: "final bytes", readErr: io.EOF, writeLimit: -1, want: "final bytes"},
		{name: "multiple reads with EOF", input: large, readErr: io.EOF, writeLimit: -1, want: large},
		{name: "data with terminal error", input: "final bytes", readErr: failure, writeLimit: -1, want: "final bytes", wantErr: failure},
		{name: "write failure", input: "message", writeErr: failure, writeLimit: -1, wantErr: failure},
		{name: "read failure", readErr: failure, writeLimit: -1, wantErr: failure},
		{name: "short write", input: "message", writeLimit: 1, want: "m", wantErr: io.ErrShortWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream := &echoStreamFixture{input: []byte(test.input), readErr: test.readErr, writeErr: test.writeErr, writeLimit: test.writeLimit}
			err := echoStream(stream)
			if !errors.Is(err, test.wantErr) {
				t.Errorf("echoStream() error = %v, want %v", err, test.wantErr)
			}
			if got := stream.output.String(); got != test.want {
				t.Errorf("echoed data differs: got %d bytes, want %d", len(got), len(test.want))
			}
			if !stream.closed {
				t.Error("stream remains open")
			}
		})
	}
}
