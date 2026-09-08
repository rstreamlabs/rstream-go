// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

func TestRunClientWindowsTerminalGeometry(t *testing.T) {
	if os.Getenv("RSTREAM_TEST_PRIVATE_CONSOLE") != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRunClientWindowsTerminalGeometry$", "-test.v")
		command.Env = append(os.Environ(), "RSTREAM_TEST_PRIVATE_CONSOLE=1")
		command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("private console client: %v\n%s", err, output)
		}
		return
	}
	stdin, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	console, err := os.Open("CONOUT$")
	if err != nil {
		t.Fatal(err)
	}
	defer console.Close()
	cols, rows, err := term.GetSize(int(console.Fd()))
	if err != nil || cols <= 0 || rows <= 0 {
		t.Fatalf("console geometry = %dx%d, %v", cols, rows, err)
	}
	var initialMode uint32
	if err := windows.GetConsoleMode(windows.Handle(stdin.Fd()), &initialMode); err != nil {
		t.Fatal(err)
	}
	t.Run("output handle ownership", func(t *testing.T) {
		fd, closeOutput, err := terminalOutputDescriptor(int(stdin.Fd()))
		if err != nil {
			t.Fatal(err)
		}
		if closeOutput == nil || fd == int(stdin.Fd()) {
			t.Fatal("terminal output must own a separate handle")
		}
		if err := closeOutput(); err != nil {
			t.Fatal(err)
		}
		var mode uint32
		if err := windows.GetConsoleMode(windows.Handle(fd), &mode); !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
			t.Fatalf("released output handle is still usable: %v", err)
		}
		if err := windows.GetConsoleMode(windows.Handle(stdin.Fd()), &mode); err != nil || mode != initialMode {
			t.Fatalf("output release changed borrowed input: mode=%d error=%v", mode, err)
		}
	})
	for _, test := range []struct {
		name        string
		interactive bool
		cancel      bool
	}{
		{name: "redirected output"},
		{name: "interactive close", interactive: true},
		{name: "interactive cancellation", interactive: true, cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			sizes := make(chan *pb.TerminalSize, 1)
			server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
				defer conn.Close()
				readWebTTYMessage(t, conn)
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
				size := readWebTTYMessage(t, conn).GetParameter().GetTerminalSize()
				sizes <- size
				if test.cancel {
					cancel()
					_, _, _ = conn.ReadMessage()
					return
				}
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Data{Data: []byte("console output")}}}})
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 7}}})
			})
			defer server.Close()
			var stdout, stderr bytes.Buffer
			code, err := RunClient(ctx, &ClientConfig{URL: testWebTTYURL(server.URL), AllocateTTY: true, Interactive: test.interactive, Stdin: stdin, Stdout: &stdout, Stderr: &stderr})
			if test.cancel {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled client = %d, %v", code, err)
				}
			} else if err != nil || code != 7 || stdout.String() != "console output" || stderr.Len() != 0 {
				t.Fatalf("client = %d, %v, stdout=%q stderr=%q", code, err, stdout.String(), stderr.String())
			}
			select {
			case size := <-sizes:
				if int(size.GetRow()) != rows || int(size.GetCol()) != cols {
					t.Fatalf("received geometry = %v, want %dx%d", size, cols, rows)
				}
			default:
				t.Fatal("client did not send the console geometry")
			}
			var mode uint32
			if err := windows.GetConsoleMode(windows.Handle(stdin.Fd()), &mode); err != nil || mode != initialMode {
				t.Fatalf("borrowed console mode was not restored: mode=%d error=%v", mode, err)
			}
		})
	}
}
