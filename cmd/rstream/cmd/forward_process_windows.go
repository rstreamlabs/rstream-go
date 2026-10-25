// See LICENSE file in the project root for license information.

package cmd

import "errors"

func newForwardProcessGroup() error {
	return errors.New("--new-process-group requires a Unix host")
}
