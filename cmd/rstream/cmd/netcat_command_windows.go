// See LICENSE file in the project root for license information.

//go:build windows

package cmd

import (
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func netcatShellCommand(command string) (string, []string) {
	shell := strings.TrimSpace(os.Getenv("COMSPEC"))
	if shell == "" {
		shell = "cmd.exe"
	}
	return shell, []string{"/C", command}
}

func splitNetcatCommand(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	ptr, err := windows.UTF16PtrFromString(command)
	if err != nil {
		return nil
	}
	var argc int32
	argv, err := windows.CommandLineToArgv(ptr, &argc)
	if err != nil || argc <= 0 {
		return nil
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(argv)))
	args := make([]string, 0, argc)
	for i := int32(0); i < argc; i++ {
		args = append(args, windows.UTF16ToString(argv[i][:]))
	}
	return args
}
