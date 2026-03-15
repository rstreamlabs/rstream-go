// See LICENSE file in the project root for license information.

//go:build darwin

package webtty

import (
	"bytes"
	"fmt"
	"os/exec"
	"os/user"
	"strings"
)

func DefaultShell(usr *user.User) (string, error) {
	path := "/Users/" + usr.Username
	cmd := exec.Command("dscl", ".", "-read", path, "UserShell")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("dscl read %s: %w (%s)", path, err, stderr.String())
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "UserShell:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "UserShell:")), nil
		}
	}
	return "", fmt.Errorf("unexpected dscl output: %s", out)
}
