// See LICENSE file in the project root for license information.

//go:build !windows && !linux && !darwin

package webtty

func addUnixLoginSessionEnvironment(_ *[]string, _ *UserInfo) {}
