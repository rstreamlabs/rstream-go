// See LICENSE file in the project root for license information.

package cmd

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

func openBrowser(target string) error {
	if err := validateBrowserTarget(target); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}

func validateBrowserTarget(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("browser target is empty")
	}
	for _, r := range target {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("browser target contains control characters")
		}
	}
	u, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("invalid browser target: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("browser target scheme %q is not allowed", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("browser target host is empty")
	}
	return nil
}
