// See LICENSE file in the project root for license information.

//go:build !windows && !darwin

package webtty

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"strings"
)

func DefaultShell(usr *user.User) (string, error) {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return "", fmt.Errorf("open /etc/passwd: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), ":", 7)
		if len(parts) == 7 && parts[2] == usr.Uid {
			return parts[6], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan /etc/passwd: %w", err)
	}
	return "", fmt.Errorf("shell for uid %s not found", usr.Uid)
}
