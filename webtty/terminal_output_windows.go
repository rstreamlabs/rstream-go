// See LICENSE file in the project root for license information.

package webtty

import (
	"fmt"
	"os"
)

func terminalOutputDescriptor(_ int) (int, func() error, error) {
	// Windows size queries require a screen buffer, even when stdout is redirected.
	output, err := os.Open("CONOUT$")
	if err != nil {
		return 0, nil, fmt.Errorf("failed to open terminal screen buffer: %w", err)
	}
	return int(output.Fd()), output.Close, nil
}
