// See LICENSE file in the project root for license information.

// http-wt-matrix-client drives the WebTransport coverage matrix against
// http-wt-matrix-server. Each case opens a fresh WT session whose URL carries
// the case name as a query parameter, runs a scripted exchange, and asserts
// the expected outcome. `-case=all` walks the full matrix sequentially and
// exits non-zero on the first mismatch.

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/webtransport-go"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

// sessionDialer abstracts the two dial paths (rstream SDK vs direct public
// host) so every case can reuse the same per-session plumbing.
type sessionDialer func(ctx context.Context, relPath string) (*webtransport.Session, error)

const tunneledQUICInitialPacketSize = 1200

// buildDialer returns a sessionDialer for either the published host (when
// -publish) or the rstream tunnel dialer (non-publish). Both produce fully
// set up *webtransport.Session instances; callers are responsible for close.
// token, when non-empty, is sent as "Authorization: Bearer <token>" on each
// WebTransport dial (supports token-auth protected tunnels).
func buildDialer(client *rstream.Client, publish bool, token string) (sessionDialer, error) {
	if publish {
		host, err := findPublishedHost(client, "wt-matrix")
		if err != nil {
			return nil, err
		}
		if _, _, err := net.SplitHostPort(host); err != nil {
			var addrErr *net.AddrError
			if errors.As(err, &addrErr) && addrErr.Err == "missing port in address" {
				host = net.JoinHostPort(host, "443")
			} else {
				return nil, fmt.Errorf("invalid published host %q: %w", host, err)
			}
		}
		dialHdr := http.Header{"Origin": {"https://" + host}}
		if token != "" {
			dialHdr.Set("Authorization", "Bearer "+token)
		}
		return func(ctx context.Context, relPath string) (*webtransport.Session, error) {
			d := webtransport.Dialer{}
			u := "https://" + host + relPath
			_, sess, err := d.Dial(ctx, u, dialHdr)
			return sess, err
		}, nil
	}
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	return func(ctx context.Context, relPath string) (*webtransport.Session, error) {
		d := webtransport.Dialer{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h3"}},
			DialAddr: func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
				raddr := rstream.Addr{IdOrName: addr}
				pc, err := client.PacketDial(ctx, raddr)
				if err != nil {
					return nil, err
				}
				tunneledConfig := cfg.Clone()
				tunneledConfig.InitialPacketSize = tunneledQUICInitialPacketSize
				return quic.DialEarly(ctx, pc, &raddr, tlsCfg, tunneledConfig)
			},
		}
		u := "https://wt-matrix" + relPath
		_, sess, err := d.Dial(ctx, u, http.Header{"Origin": {"https://wt-matrix"}})
		return sess, err
	}, nil
}

func findPublishedHost(client *rstream.Client, name string) (string, error) {
	tunnels, err := client.ListTunnels(context.Background(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to list tunnels: %w", err)
	}
	for _, t := range *tunnels {
		if t.Name != nil && *t.Name == name && t.Host != nil {
			return *t.Host, nil
		}
	}
	return "", fmt.Errorf("tunnel %q not found or not published", name)
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ---- per-case client implementations --------------------------------------

type testCase struct {
	name string
	run  func(ctx context.Context, dial sessionDialer) error
}

func caseBidiEcho(payload []byte) func(context.Context, sessionDialer) error {
	return func(ctx context.Context, dial sessionDialer) error {
		sess, err := dial(ctx, "/webtransport?case=bidi-echo")
		if err != nil {
			return err
		}
		defer sess.CloseWithError(0, "")
		s, err := sess.OpenStreamSync(ctx)
		if err != nil {
			return fmt.Errorf("OpenStreamSync: %w", err)
		}
		if _, err := s.Write(payload); err != nil {
			return fmt.Errorf("write: %w", err)
		}
		if err := s.Close(); err != nil {
			return fmt.Errorf("close write: %w", err)
		}
		got, err := io.ReadAll(s)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if !bytes.Equal(got, payload) {
			return fmt.Errorf("echo mismatch: got %d bytes (sha=%s), want %d bytes (sha=%s)",
				len(got), sha256hex(got), len(payload), sha256hex(payload))
		}
		return nil
	}
}

// caseBidiHalfclose writes a payload, half-closes the write side via s.Close(),
// and expects a single reply "eof-seen:<N>" where N is the byte count.
func caseBidiHalfclose(payload []byte) func(context.Context, sessionDialer) error {
	return func(ctx context.Context, dial sessionDialer) error {
		sess, err := dial(ctx, "/webtransport?case=bidi-halfclose")
		if err != nil {
			return err
		}
		defer sess.CloseWithError(0, "")
		s, err := sess.OpenStreamSync(ctx)
		if err != nil {
			return err
		}
		if _, err := s.Write(payload); err != nil {
			return err
		}
		if err := s.Close(); err != nil {
			return err
		}
		buf, err := io.ReadAll(s)
		if err != nil {
			return err
		}
		want := fmt.Sprintf("eof-seen:%d", len(payload))
		if string(buf) != want {
			return fmt.Errorf("halfclose reply mismatch: got %q, want %q", string(buf), want)
		}
		return nil
	}
}

func caseBidiMulti(n int, payloadBytes int) func(context.Context, sessionDialer) error {
	return func(ctx context.Context, dial sessionDialer) error {
		sess, err := dial(ctx, fmt.Sprintf("/webtransport?case=bidi-multi&n=%d", n))
		if err != nil {
			return err
		}
		defer sess.CloseWithError(0, "")
		var wg sync.WaitGroup
		errCh := make(chan error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				payload := make([]byte, payloadBytes)
				// Fill with a per-stream marker so a crossed-wires bug shows up.
				for k := range payload {
					payload[k] = byte(i*31 + k)
				}
				s, err := sess.OpenStreamSync(ctx)
				if err != nil {
					errCh <- fmt.Errorf("stream %d OpenStreamSync: %w", i, err)
					return
				}
				if _, err := s.Write(payload); err != nil {
					errCh <- fmt.Errorf("stream %d write: %w", i, err)
					return
				}
				if err := s.Close(); err != nil {
					errCh <- fmt.Errorf("stream %d close: %w", i, err)
					return
				}
				got, err := io.ReadAll(s)
				if err != nil {
					errCh <- fmt.Errorf("stream %d read: %w", i, err)
					return
				}
				if !bytes.Equal(got, payload) {
					errCh <- fmt.Errorf("stream %d echo mismatch (len=%d vs %d)", i, len(got), len(payload))
				}
			}(i)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			if err != nil {
				return err
			}
		}
		return nil
	}
}

// caseUniC2S opens a uni stream, writes payload, Closes, then reads the
// server's report on a server-initiated bidi. Exercises both directions
// (client uni → server, server bidi → client) on one session.
func caseUniC2S(payload []byte) func(context.Context, sessionDialer) error {
	return func(ctx context.Context, dial sessionDialer) error {
		sess, err := dial(ctx, "/webtransport?case=uni-c2s")
		if err != nil {
			return err
		}
		defer sess.CloseWithError(0, "")
		us, err := sess.OpenUniStreamSync(ctx)
		if err != nil {
			return fmt.Errorf("OpenUniStreamSync: %w", err)
		}
		if _, err := us.Write(payload); err != nil {
			return err
		}
		if err := us.Close(); err != nil {
			return err
		}
		report, err := sess.AcceptStream(ctx)
		if err != nil {
			return fmt.Errorf("AcceptStream (report): %w", err)
		}
		buf, err := io.ReadAll(report)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		want := fmt.Sprintf("uni-received:len=%d:sha=%s", len(payload), sha256hex(payload))
		if string(buf) != want {
			return fmt.Errorf("uni report mismatch: got %q, want %q", string(buf), want)
		}
		return nil
	}
}

// caseUniS2C synchronizes on a control bidi ("go"), reads the server's uni
// payload, and acks. Tests server-initiated uni streams across the relay.
func caseUniS2C() func(context.Context, sessionDialer) error {
	return func(ctx context.Context, dial sessionDialer) error {
		sess, err := dial(ctx, "/webtransport?case=uni-s2c")
		if err != nil {
			return err
		}
		defer sess.CloseWithError(0, "")
		ctrl, err := sess.OpenStreamSync(ctx)
		if err != nil {
			return err
		}
		if _, err := ctrl.Write([]byte("go")); err != nil {
			return err
		}
		rs, err := sess.AcceptUniStream(ctx)
		if err != nil {
			return fmt.Errorf("AcceptUniStream: %w", err)
		}
		got, err := io.ReadAll(rs)
		if err != nil {
			return err
		}
		if string(got) != "server-uni-payload" {
			return fmt.Errorf("uni-s2c payload mismatch: got %q", string(got))
		}
		if err := ctrl.Close(); err != nil {
			return err
		}
		ack, err := io.ReadAll(ctrl)
		if err != nil {
			return err
		}
		if string(ack) != "sent" {
			return fmt.Errorf("uni-s2c ack mismatch: got %q", string(ack))
		}
		return nil
	}
}

// caseDatagram sends N datagrams and verifies *at least* half echo back. QUIC
// datagrams are unreliable and may be dropped under load — we don't require
// exact delivery, just that the relay doesn't swallow everything.
func caseDatagram(n int, size int) func(context.Context, sessionDialer) error {
	return func(ctx context.Context, dial sessionDialer) error {
		sess, err := dial(ctx, fmt.Sprintf("/webtransport?case=datagram&n=%d", n))
		if err != nil {
			return err
		}
		defer sess.CloseWithError(0, "")
		sent := make([][]byte, n)
		for i := 0; i < n; i++ {
			p := make([]byte, size)
			p[0] = byte(i)
			if _, err := rand.Read(p[1:]); err != nil {
				return err
			}
			sent[i] = p
			if err := sess.SendDatagram(p); err != nil {
				return fmt.Errorf("SendDatagram[%d]: %w", i, err)
			}
		}
		// Collect echoes up to a deadline.
		deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		seen := map[string]bool{}
		for len(seen) < n {
			payload, err := sess.ReceiveDatagram(deadline)
			if err != nil {
				break
			}
			seen[string(payload)] = true
		}
		hit := 0
		for _, p := range sent {
			if seen[string(p)] {
				hit++
			}
		}
		if hit < n/2 {
			return fmt.Errorf("datagram loss too high: %d/%d echoed back", hit, n)
		}
		return nil
	}
}

// caseServerBidi synchronizes on "go", then accepts a server-opened bidi,
// echoes its content back on the same stream, and verifies the "got:<echo>"
// report on the control stream.
func caseServerBidi() func(context.Context, sessionDialer) error {
	return func(ctx context.Context, dial sessionDialer) error {
		sess, err := dial(ctx, "/webtransport?case=server-bidi")
		if err != nil {
			return err
		}
		defer sess.CloseWithError(0, "")
		ctrl, err := sess.OpenStreamSync(ctx)
		if err != nil {
			return err
		}
		if _, err := ctrl.Write([]byte("go")); err != nil {
			return err
		}
		s, err := sess.AcceptStream(ctx)
		if err != nil {
			return fmt.Errorf("AcceptStream (server-bidi): %w", err)
		}
		got, err := io.ReadAll(s)
		if err != nil {
			return err
		}
		if string(got) != "hello-from-server" {
			return fmt.Errorf("server-bidi payload mismatch: %q", string(got))
		}
		if _, err := s.Write(got); err != nil {
			return err
		}
		if err := s.Close(); err != nil {
			return err
		}
		if err := ctrl.Close(); err != nil {
			return err
		}
		report, err := io.ReadAll(ctrl)
		if err != nil {
			return err
		}
		want := "got:" + string(got)
		if string(report) != want {
			return fmt.Errorf("server-bidi report mismatch: got %q, want %q", string(report), want)
		}
		return nil
	}
}

// caseCloseCode asserts that webtransport.SessionError propagates with the
// server-supplied code AND reason through the relay (a critical regression
// surface — the engine tears down only if propagation works end-to-end).
func caseCloseCode(code uint32, reason string) func(context.Context, sessionDialer) error {
	return func(ctx context.Context, dial sessionDialer) error {
		sess, err := dial(ctx, fmt.Sprintf("/webtransport?case=close-code&n=%d&reason=%s", code, reason))
		if err != nil {
			return err
		}
		defer sess.CloseWithError(0, "")
		ctrl, err := sess.OpenStreamSync(ctx)
		if err != nil {
			return err
		}
		if _, err := ctrl.Write([]byte("go")); err != nil {
			return err
		}
		// Read ack BEFORE signalling "done" — the server only closes the
		// session after receiving "done", so buffered stream data is flushed.
		buf := make([]byte, 16)
		n, err := io.ReadFull(ctrl, buf[:3])
		if err != nil {
			return fmt.Errorf("ack read: %w", err)
		}
		if string(buf[:n]) != "ack" {
			return fmt.Errorf("unexpected ack %q", string(buf[:n]))
		}
		if _, err := ctrl.Write([]byte("done")); err != nil {
			return fmt.Errorf("done write: %w", err)
		}
		// The server now closes the session. In webtransport-go the SessionError
		// is surfaced via session-level calls (Accept*, Open*, ReceiveDatagram)
		// after close — reading a stream that was already half-closed by the
		// peer yields a StreamError instead, so we probe with AcceptStream and
		// let it return the session's close error.
		var sessErr *webtransport.SessionError
		_, err = sess.AcceptStream(ctx)
		if err == nil {
			return fmt.Errorf("expected SessionError after server close, got nil")
		}
		if !errors.As(err, &sessErr) {
			return fmt.Errorf("expected *webtransport.SessionError, got %T: %v", err, err)
		}
		if uint32(sessErr.ErrorCode) != code {
			return fmt.Errorf("close code mismatch: got %d, want %d", sessErr.ErrorCode, code)
		}
		if sessErr.Message != reason {
			return fmt.Errorf("close reason mismatch: got %q, want %q", sessErr.Message, reason)
		}
		return nil
	}
}

// caseCombo runs bidi + uni + datagrams concurrently on a single session.
func caseCombo() func(context.Context, sessionDialer) error {
	return func(ctx context.Context, dial sessionDialer) error {
		sess, err := dial(ctx, "/webtransport?case=combo&n=4")
		if err != nil {
			return err
		}
		defer sess.CloseWithError(0, "")
		var wg sync.WaitGroup
		errCh := make(chan error, 3)
		// Bidi echo
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := sess.OpenStreamSync(ctx)
			if err != nil {
				errCh <- err
				return
			}
			payload := []byte(strings.Repeat("abc", 300))
			if _, err := s.Write(payload); err != nil {
				errCh <- err
				return
			}
			if err := s.Close(); err != nil {
				errCh <- err
				return
			}
			got, err := io.ReadAll(s)
			if err != nil {
				errCh <- err
				return
			}
			if !bytes.Equal(got, payload) {
				errCh <- fmt.Errorf("combo bidi mismatch")
			}
		}()
		// Uni drain (fire & forget — we only check the relay doesn't choke)
		wg.Add(1)
		go func() {
			defer wg.Done()
			us, err := sess.OpenUniStreamSync(ctx)
			if err != nil {
				errCh <- err
				return
			}
			if _, err := us.Write([]byte("uni-combo")); err != nil {
				errCh <- err
				return
			}
			_ = us.Close()
		}()
		// Datagrams — best-effort
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 16; i++ {
				if err := sess.SendDatagram([]byte{byte(i)}); err != nil {
					errCh <- err
					return
				}
			}
			deadline, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			seen := 0
			for seen < 4 { // tolerate high loss; just ensure >0
				if _, err := sess.ReceiveDatagram(deadline); err != nil {
					break
				}
				seen++
			}
			if seen == 0 {
				errCh <- fmt.Errorf("no datagrams echoed")
			}
		}()
		wg.Wait()
		close(errCh)
		for err := range errCh {
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func allCases() []testCase {
	large := make([]byte, 256*1024)
	_, _ = rand.Read(large)
	return []testCase{
		{"bidi-echo", caseBidiEcho([]byte("ping"))},
		{"bidi-large", caseBidiEcho(large)},
		{"bidi-halfclose", caseBidiHalfclose([]byte("halfclose-payload"))},
		{"bidi-multi", caseBidiMulti(8, 4096)},
		{"uni-c2s", caseUniC2S([]byte("uni-c2s-payload"))},
		{"uni-s2c", caseUniS2C()},
		{"datagram", caseDatagram(32, 128)},
		{"server-bidi", caseServerBidi()},
		{"close-code", caseCloseCode(4242, "goodbye")},
		{"combo", caseCombo()},
	}
}

func main() {
	publish := flag.Bool("publish", false, "dial the published host instead of the rstream SDK path")
	caseName := flag.String("case", "all", "test case name, or 'all' to run the full matrix")
	timeout := flag.Duration("timeout", 30*time.Second, "per-case timeout")
	token := flag.String("token", "", "Bearer token for token-auth protected tunnels (empty = no auth header)")
	flag.Parse()
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	dial, err := buildDialer(client, *publish, *token)
	if err != nil {
		log.Fatalf("dialer setup: %v", err)
	}
	cases := allCases()
	if *caseName != "all" {
		filtered := make([]testCase, 0, 1)
		for _, tc := range cases {
			if tc.name == *caseName {
				filtered = append(filtered, tc)
				break
			}
		}
		if len(filtered) == 0 {
			log.Fatalf("unknown case %q (known: %s)", *caseName, listNames(cases))
		}
		cases = filtered
	}
	var failed []string
	for _, tc := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		start := time.Now()
		err := tc.run(ctx, dial)
		elapsed := time.Since(start)
		cancel()
		if err != nil {
			failed = append(failed, tc.name)
			fmt.Printf("FAIL %-20s (%.2fs): %v\n", tc.name, elapsed.Seconds(), err)
		} else {
			fmt.Printf("PASS %-20s (%.2fs)\n", tc.name, elapsed.Seconds())
		}
	}
	fmt.Printf("---- summary: %d passed, %d failed out of %d ----\n", len(cases)-len(failed), len(failed), len(cases))
	if len(failed) > 0 {
		os.Exit(1)
	}
}

func listNames(cases []testCase) string {
	names := make([]string, len(cases))
	for i, tc := range cases {
		names[i] = tc.name
	}
	return strings.Join(names, ", ")
}
