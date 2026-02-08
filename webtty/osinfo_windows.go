// See LICENSE file in the project root for license information.

//go:build windows
// +build windows

package webtty

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func getOSDetails() osDetails {
	d := osDetails{id: "windows"}
	d.hostname, _ = os.Hostname()
	version, pretty := windowsVersion()
	d.versionID = version
	d.kernel = version
	d.prettyName = pretty
	return d
}

func windowsVersion() (string, string) {
	info := windows.RtlGetVersion()
	version := fmt.Sprintf("%d.%d.%d",
		info.MajorVersion,
		info.MinorVersion,
		info.BuildNumber,
	)
	product, display := windowsProductInfo()
	switch {
	case product != "" && display != "":
		return version, product + " " + display
	case product != "":
		return version, product
	default:
		return version, ""
	}
}

func windowsProductInfo() (string, string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return "", ""
	}
	defer k.Close()
	product, _, _ := k.GetStringValue("ProductName")
	display, _, _ := k.GetStringValue("DisplayVersion")
	if display == "" {
		display, _, _ = k.GetStringValue("ReleaseId")
	}
	if display == "" {
		display, _, _ = k.GetStringValue("CSDVersion")
	}
	return product, display
}
