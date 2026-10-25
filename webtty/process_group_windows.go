// See LICENSE file in the project root for license information.

package webtty

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsChildProcessGroup struct {
	mu      sync.Mutex
	job     windows.Handle
	process windows.Handle
}

func ownChildProcess(_ *exec.Cmd) childProcessGroup {
	return nil
}

func newWindowsChildProcessGroup() (*windowsChildProcessGroup, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	return &windowsChildProcessGroup{job: job}, nil
}

func startChildProcess(cmd *exec.Cmd) (childProcessGroup, error) {
	group, err := newWindowsChildProcessGroup()
	if err != nil {
		return nil, fmt.Errorf("create WebTTY process job: %w", err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := cmd.Start(); err != nil {
		_ = group.close()
		return nil, err
	}
	if err := group.assignAndResume(uint32(cmd.Process.Pid)); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = group.close()
		return nil, fmt.Errorf("own WebTTY process before execution: %w", err)
	}
	return group, nil
}

func (g *windowsChildProcessGroup) assignAndResume(pid uint32) error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return err
	}
	defer func() {
		if g.process == 0 {
			_ = windows.CloseHandle(process)
		}
	}()
	if err := windows.AssignProcessToJobObject(g.job, process); err != nil {
		return err
	}
	threadID, err := suspendedChildThread(pid)
	if err != nil {
		return err
	}
	thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, threadID)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(thread)
	previous, err := windows.ResumeThread(thread)
	if err != nil {
		return err
	}
	if previous != 1 {
		return errors.New("unexpected WebTTY child suspension state")
	}
	g.process = process
	return nil
}

func (g *windowsChildProcessGroup) waitForExit() error {
	g.mu.Lock()
	var process windows.Handle
	err := windows.DuplicateHandle(windows.CurrentProcess(), g.process, windows.CurrentProcess(), &process, windows.SYNCHRONIZE, false, 0)
	g.mu.Unlock()
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	result, err := windows.WaitForSingleObject(process, windows.INFINITE)
	if err != nil {
		return err
	}
	if result != windows.WAIT_OBJECT_0 {
		return errors.New("unexpected WebTTY process wait result")
	}
	return g.kill()
}

func suspendedChildThread(pid uint32) (uint32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	threadID := uint32(0)
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID == pid {
			if threadID != 0 {
				return 0, errors.New("unexpected threads in suspended WebTTY child")
			}
			threadID = entry.ThreadID
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return 0, err
	}
	if threadID == 0 {
		return 0, errors.New("suspended WebTTY child thread is missing")
	}
	return threadID, nil
}

func (g *windowsChildProcessGroup) interrupt() error {
	return g.kill()
}

func (g *windowsChildProcessGroup) kill() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job == 0 {
		return nil
	}
	return windows.TerminateJobObject(g.job, windowsInterruptedExitCode)
}

func (g *windowsChildProcessGroup) close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	var err error
	if g.job != 0 {
		err = windows.CloseHandle(g.job)
		if err == nil {
			g.job = 0
		}
	}
	if g.process != 0 {
		processErr := windows.CloseHandle(g.process)
		if processErr == nil {
			g.process = 0
		}
		err = errors.Join(err, processErr)
	}
	return err
}
