// See LICENSE file in the project root for license information.

package webtty

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestDarwinTerminationPreservesResultWithMultipleZombies(t *testing.T) {
	leader := exec.Command("/bin/sh", "-c", "exit 23")
	leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	defer leader.Wait()
	if err := waitChildExited(leader.Process.Pid); err != nil {
		t.Fatal(err)
	}
	input, release, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer release.Close()
	member := exec.Command("/bin/sh", "-c", "cat >/dev/null; exit 24")
	member.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: leader.Process.Pid}
	member.Stdin = input
	if err := member.Start(); err != nil {
		t.Fatal(err)
	}
	defer member.Wait()
	defer member.Process.Kill()
	if processGroupIsEmpty(leader.Process.Pid, syscall.EPERM) {
		t.Fatal("live group member incorrectly treated as a zombie")
	}
	if err := release.Close(); err != nil {
		t.Fatal(err)
	}
	if err := waitChildExited(member.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-leader.Process.Pid, syscall.SIGKILL); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("expected Darwin zombie group EPERM, got %v", err)
	}
	group := ownChildProcess(leader)
	if err := group.kill(); err != nil {
		t.Fatalf("termination failed for exited group: %v", err)
	}
	if err := group.waitForExit(); err != nil {
		t.Fatal(err)
	}
	if err := leader.Wait(); err == nil || leader.ProcessState.ExitCode() != 23 {
		t.Fatalf("leader result changed: %v, %v", leader.ProcessState, err)
	}
	if err := member.Wait(); err == nil || member.ProcessState.ExitCode() != 24 {
		t.Fatalf("member result changed: %v, %v", member.ProcessState, err)
	}
}
