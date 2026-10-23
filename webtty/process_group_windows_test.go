// See LICENSE file in the project root for license information.

package webtty

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsProcessGroupClosesRetainedProcessWithoutJob(t *testing.T) {
	var process windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.CurrentProcess(), windows.CurrentProcess(), &process, windows.SYNCHRONIZE, false, 0); err != nil {
		t.Fatal(err)
	}
	group := &windowsChildProcessGroup{process: process}
	defer windows.CloseHandle(process)
	if err := group.close(); err != nil {
		t.Fatal(err)
	}
	if group.process != 0 {
		t.Fatal("process handle retained after the job was already closed")
	}
	if _, err := windows.WaitForSingleObject(process, 0); !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		t.Fatalf("retained process handle is still open: %v", err)
	}
	if err := group.close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
}

func TestWindowsWebTTYJobOwnsDescendants(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestWebTTYTerminationProcess$")
	cmd.Env = append(os.Environ(), "RSTREAM_WEBTTY_TERMINATION_HELPER=parent")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	group, err := startChildProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer group.close()
	timer := time.AfterFunc(5*time.Second, func() { _ = group.kill() })
	defer timer.Stop()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("descendant did not become ready: %v", err)
	}
	pid, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "descendant:")), 10, 32)
	if err != nil {
		t.Fatalf("invalid descendant readiness %q: %v", line, err)
	}
	child, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(child)
	if err := group.interrupt(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil || uint32(cmd.ProcessState.ExitCode()) != windowsInterruptedExitCode {
		t.Fatalf("unexpected native termination: %v, %v", cmd.ProcessState, err)
	}
	result, err := windows.WaitForSingleObject(child, 5000)
	if err != nil || result != windows.WAIT_OBJECT_0 {
		t.Fatalf("descendant outlived its job: %d, %v", result, err)
	}
	if err := group.kill(); err != nil {
		t.Fatalf("repeated termination: %v", err)
	}
	if err := group.close(); err != nil {
		t.Fatal(err)
	}
}
