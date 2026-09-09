// See LICENSE file in the project root for license information.

//go:build !windows

package webtty

func terminalOutputDescriptor(stdinFD int) (int, func() error, error) {
	return stdinFD, nil, nil
}
