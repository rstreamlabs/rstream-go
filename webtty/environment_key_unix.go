// See LICENSE file in the project root for license information.

//go:build !windows

package webtty

func environmentKeyEqual(left, right string) bool {
	return left == right
}
