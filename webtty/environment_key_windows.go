// See LICENSE file in the project root for license information.

//go:build windows

package webtty

import "strings"

func environmentKeyEqual(left, right string) bool {
	return strings.EqualFold(left, right)
}
