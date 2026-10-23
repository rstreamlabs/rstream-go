// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"google.golang.org/protobuf/proto"
)

const maxTerminationGrace = 30 * time.Second

var ErrTerminationUnsupported = errors.New("WebTTY server does not support explicit termination")

func (c *clientRuntime) writeTermination(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.writeMu.TryLock() {
		return errClientWriteBusy
	}
	defer c.writeMu.Unlock()
	deadline := c.closeWriteDeadline()
	if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(deadline) {
		deadline = callerDeadline
	}
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	cancelled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(cancelled)
		_ = c.conn.Close()
	})
	defer func() {
		if !stop() {
			<-cancelled
		}
		_ = c.conn.SetWriteDeadline(time.Time{})
	}()
	payload, err := proto.Marshal(&pb.Message{Payload: &pb.Message_Terminate{Terminate: &pb.Terminate{}}})
	if err != nil {
		return err
	}
	err = c.conn.WriteMessage(websocket.BinaryMessage, payload)
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (s *session) handleTerminate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.opened || s.cmd == nil || s.cmd.Process == nil {
		return errors.New("WebTTY termination requires an open process")
	}
	if s.childDone || s.terminationReq {
		return nil
	}
	if s.processGroup == nil {
		return ErrTerminationUnsupported
	}
	s.terminationReq = true
	cmd, ptyFile := s.cmd, s.ptyFile
	processGroup := s.processGroup
	s.startLoopLocked(func() { s.terminateProcess(cmd, ptyFile, processGroup) })
	return nil
}

func (s *session) terminationGrace() time.Duration {
	grace := defaultSessionCloseDeadline
	if s.cfg != nil && s.cfg.SessionCloseDeadline != nil && *s.cfg.SessionCloseDeadline > 0 {
		grace = min(*s.cfg.SessionCloseDeadline, maxTerminationGrace)
	}
	return grace
}

func (s *session) terminateProcess(cmd *exec.Cmd, ptyFile *os.File, group childProcessGroup) {
	if err := requestChildShutdown(cmd, ptyFile, group); err != nil && !errors.Is(err, os.ErrProcessDone) {
		s.logger.Warn("WebTTY process termination request failed", "operation", "interrupt_process")
		if err := killOwnedChild(cmd, group); err != nil && !errors.Is(err, os.ErrProcessDone) {
			s.error(fmt.Errorf("failed to terminate WebTTY process: %w", err))
			return
		}
	}
	timer := time.NewTimer(s.terminationGrace())
	defer timer.Stop()
	select {
	case <-s.childExited:
		return
	case <-s.ctx.Done():
		return
	case <-timer.C:
	}
	if err := killOwnedChild(cmd, group); err != nil && !errors.Is(err, os.ErrProcessDone) {
		s.error(fmt.Errorf("failed to force terminate WebTTY process: %w", err))
		return
	}
	timer.Reset(s.terminationGrace())
	select {
	case <-s.childExited:
	case <-s.ctx.Done():
	case <-timer.C:
		s.error(errors.New("WebTTY process termination result timed out"))
	}
}
