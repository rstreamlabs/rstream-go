// See LICENSE file in the project root for license information.

// Package quicstream provides net.Conn write and close semantics for QUIC streams.
package quicstream

import (
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

type Stream struct {
	*quic.Stream
	writeMu     sync.Mutex
	deadlineMu  sync.Mutex
	closed      bool
	writeClosed bool
	readCode    quic.StreamErrorCode
}

func New(stream *quic.Stream, readCode quic.StreamErrorCode) *Stream {
	return &Stream{Stream: stream, readCode: readCode}
}

func (s *Stream) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.deadlineMu.Lock()
	closed := s.writeClosed
	s.deadlineMu.Unlock()
	if closed {
		return 0, net.ErrClosed
	}
	return s.Stream.Write(p)
}

func (s *Stream) Close() error {
	s.deadlineMu.Lock()
	if s.closed {
		s.deadlineMu.Unlock()
		return nil
	}
	s.closed = true
	s.writeClosed = true
	s.Stream.CancelRead(s.readCode)
	_ = s.Stream.SetWriteDeadline(time.Now())
	s.deadlineMu.Unlock()
	// Wake the current writer before waiting for it. QUIC Close must not run
	// concurrently with Write; FIN then preserves bytes already accepted.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.Stream.Close()
}

func (s *Stream) CloseWrite() error {
	s.deadlineMu.Lock()
	if s.writeClosed {
		s.deadlineMu.Unlock()
		return nil
	}
	s.writeClosed = true
	_ = s.Stream.SetWriteDeadline(time.Now())
	s.deadlineMu.Unlock()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.Stream.Close()
}

func (s *Stream) SetDeadline(deadline time.Time) error {
	s.deadlineMu.Lock()
	defer s.deadlineMu.Unlock()
	if s.closed {
		return net.ErrClosed
	}
	if s.writeClosed {
		return s.Stream.SetReadDeadline(deadline)
	}
	return s.Stream.SetDeadline(deadline)
}

func (s *Stream) SetWriteDeadline(deadline time.Time) error {
	s.deadlineMu.Lock()
	defer s.deadlineMu.Unlock()
	if s.writeClosed {
		return net.ErrClosed
	}
	return s.Stream.SetWriteDeadline(deadline)
}
