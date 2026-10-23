// See LICENSE file in the project root for license information.

//go:build !windows && !linux && !darwin

package webtty

const processGroupSupported = false

func waitChildExited(_ int) error {
	return ErrTerminationUnsupported
}

func processGroupIsEmpty(_ int, _ error) bool {
	return false
}
