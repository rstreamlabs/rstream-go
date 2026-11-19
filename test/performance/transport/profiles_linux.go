//go:build linux

// See LICENSE file in the project root for license information.

package main

import (
	"errors"
	"os"
	"runtime/pprof"
)

func startProfiles(prefix string) (func() error, error) {
	if prefix == "" {
		return func() error { return nil }, nil
	}
	cpu, err := os.OpenFile(prefix+"-cpu.pprof", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	allocations, err := os.OpenFile(prefix+"-allocs.pprof", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, errors.Join(err, cpu.Close())
	}
	if err := pprof.StartCPUProfile(cpu); err != nil {
		return nil, errors.Join(err, cpu.Close(), allocations.Close())
	}
	return func() error {
		pprof.StopCPUProfile()
		return errors.Join(cpu.Close(), pprof.Lookup("allocs").WriteTo(allocations, 0), allocations.Close())
	}, nil
}
