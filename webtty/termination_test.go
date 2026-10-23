// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"google.golang.org/protobuf/proto"
)

func TestWebTTYTerminationProcess(t *testing.T) {
	if os.Getenv("RSTREAM_WEBTTY_TERMINATION_HELPER") == "descendant" {
		signal.Ignore(os.Interrupt)
		if address := os.Getenv("RSTREAM_WEBTTY_DESCENDANT_MONITOR"); address != "" {
			runMonitoredTerminationDescendant(address)
			return
		}
		fmt.Fprintf(os.Stdout, "descendant:%d\n", os.Getpid())
		time.Sleep(time.Hour)
		os.Exit(1)
	}
	if os.Getenv("RSTREAM_WEBTTY_TERMINATION_HELPER") == "natural-parent" {
		runMonitoredTerminationParent()
		return
	}
	if os.Getenv("RSTREAM_WEBTTY_TERMINATION_HELPER") == "parent" {
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt)
		child := exec.Command(os.Args[0], "-test.run=^TestWebTTYTerminationProcess$")
		child.Env = append(os.Environ(), "RSTREAM_WEBTTY_TERMINATION_HELPER=descendant")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		<-interrupt
		fmt.Fprint(os.Stdout, "final parent output\n")
		os.Exit(23)
	}
	if os.Getenv("RSTREAM_WEBTTY_TERMINATION_HELPER") != "1" {
		return
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	fmt.Fprint(os.Stdout, "ready\n")
	<-interrupt
	fmt.Fprint(os.Stdout, "final stdout\n")
	fmt.Fprint(os.Stderr, "final stderr\n")
	os.Exit(23)
}

func TestClientTerminationCancellationInterruptsBlockedWrite(t *testing.T) {
	conn := newBlockingSessionMessageConn()
	defer conn.Close()
	runtime := &clientRuntime{conn: conn, cfg: &ClientConfig{}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.writeTermination(ctx) }()
	select {
	case <-conn.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("termination write did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation result = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled termination retained its writer")
	}
}

func TestClientSessionNativeResultWinsOverPendingLocalClose(t *testing.T) {
	session := &ClientSession{
		loopCancel:  func() {},
		doneRead:    make(chan struct{}),
		done:        make(chan struct{}),
		events:      make(chan ClientSessionEvent),
		closeResult: &clientSessionResult{exitCode: -1, err: context.Canceled},
	}
	session.finalize(23, nil)
	if code, err := session.Wait(); err != nil || code != 23 {
		t.Fatalf("confirmed result overwritten by local close: %d, %v", code, err)
	}
}

func TestClientTerminationRequiresCapabilityAndKeepsNativeResult(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(fmt.Sprintf("supported=%t", supported), func(t *testing.T) {
			received := make(chan *pb.Message, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
				defer conn.Close()
				readWebTTYMessage(t, conn)
				ack := &pb.Ack{}
				if supported {
					ack.Capabilities = []pb.SessionCapability{pb.SessionCapability_SESSION_CAPABILITY_TERMINATE}
				}
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: ack}})
				if supported {
					received <- readWebTTYMessage(t, conn)
				}
				<-release
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 29}}})
			})
			defer server.Close()
			defer unblock()
			session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL)})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if session.CanTerminate() != supported {
				t.Fatalf("termination capability = %t", session.CanTerminate())
			}
			err = session.RequestTermination(t.Context())
			if !supported {
				if !errors.Is(err, ErrTerminationUnsupported) {
					t.Fatalf("legacy peer result = %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				select {
				case message := <-received:
					if message.GetTerminate() == nil {
						t.Fatalf("request = %T", message.Payload)
					}
				case <-time.After(time.Second):
					t.Fatal("termination request did not reach peer")
				}
			}
			select {
			case <-session.done:
				t.Fatal("request or rejection completed the process locally")
			default:
			}
			unblock()
			if code, err := session.Wait(); err != nil || code != 29 {
				t.Fatalf("native completion = %d, %v", code, err)
			}
		})
	}
}

func TestNativeTerminatePreservesProcessResultAndFinalOutput(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := handler.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	}()
	conn, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Open{Open: &pb.Open{Config: &pb.Config{
		CmdArgs: []string{os.Args[0], "-test.run=^TestWebTTYTerminationProcess$"},
		EnvVars: []*pb.Environment{{Key: "RSTREAM_WEBTTY_TERMINATION_HELPER", Value: "1"}},
		Options: &pb.Options{},
	}}}})
	var stdout, stderr bytes.Buffer
	terminated := false
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("transport closed without native completion: %v", err)
		}
		var message pb.Message
		if err := proto.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		if data := message.GetData(); data != nil {
			if data.Type == pb.Data_TYPE_STDOUT {
				stdout.Write(data.GetData())
			} else if data.Type == pb.Data_TYPE_STDERR {
				stderr.Write(data.GetData())
			}
			if !terminated && bytes.Contains(stdout.Bytes(), []byte("ready\n")) {
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Terminate{Terminate: &pb.Terminate{}}})
				terminated = true
			}
		}
		if result := message.GetClose(); result != nil {
			if !terminated {
				t.Fatal("process completed before the termination request")
			}
			if runtime.GOOS == "windows" {
				if result.ReturnCode == 0 {
					t.Fatal("terminated process returned a successful exit code")
				}
			} else if result.ReturnCode != 23 || stdout.String() != "ready\nfinal stdout\n" || stderr.String() != "final stderr\n" {
				t.Fatalf("native result/output lost: code=%d stdout=%q stderr=%q", result.ReturnCode, stdout.String(), stderr.String())
			}
			return
		}
	}
}
