// See LICENSE file in the project root for license information.

package rstream

import "runtime"

var (
	Agent   = "rstream"
	Channel = "dev"
	Version = "development"
	OS      = runtime.GOOS
	Arch    = runtime.GOARCH
)
