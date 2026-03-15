// See LICENSE file in the project root for license information.

//go:build windows

package webtty

import (
	"fmt"
	"os"
)

func DefaultShellWindows() (string, error) {
	if s := os.Getenv("ComSpec"); s != "" {
		return s, nil
	}
	if s := os.Getenv("COMSPEC"); s != "" {
		return s, nil
	}
	return "", fmt.Errorf("ComSpec environment variable is not set")
}
