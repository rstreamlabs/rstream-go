// See LICENSE file in the project root for license information.

//go:build !windows
// +build !windows

package webtty

import (
	"os"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

func getOSDetails() osDetails {
	d := osDetails{}
	d.hostname, _ = os.Hostname()
	switch runtime.GOOS {
	case "darwin":
		release := parseOSRelease()
		d.id = release["ID"]
		d.versionID = release["VERSION_ID"]
		d.codename = releaseCodename(release)
		d.prettyName = release["PRETTY_NAME"]
		name, version := macOSProduct()
		if d.id == "" {
			d.id = "macos"
		}
		if d.versionID == "" {
			d.versionID = version
		}
		if d.prettyName == "" {
			d.prettyName = productPretty(name, d.versionID)
		}
	default:
		release := parseOSRelease()
		d.id = release["ID"]
		d.versionID = release["VERSION_ID"]
		d.codename = releaseCodename(release)
		d.prettyName = release["PRETTY_NAME"]
		if d.prettyName == "" {
			d.prettyName = releasePrettyFallback(release)
		}
	}
	d.kernel = kernelRelease()
	if d.prettyName == "" && d.id != "" && d.versionID != "" {
		d.prettyName = d.id + " " + d.versionID
	}
	return d
}

func kernelRelease() string {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return ""
	}
	buf := make([]byte, 0, len(u.Release))
	for _, c := range u.Release {
		if c == 0 {
			break
		}
		buf = append(buf, byte(c))
	}
	return string(buf)
}

func macOSProduct() (string, string) {
	data, err := os.ReadFile("/System/Library/CoreServices/SystemVersion.plist")
	if err != nil {
		return "", ""
	}
	return plistValue(data, "ProductName"), plistValue(data, "ProductVersion")
}

func plistValue(data []byte, key string) string {
	s := string(data)
	needle := "<key>" + key + "</key>"
	i := strings.Index(s, needle)
	if i == -1 {
		return ""
	}
	rest := s[i+len(needle):]
	start := strings.Index(rest, "<string>")
	end := strings.Index(rest, "</string>")
	if start == -1 || end == -1 || end <= start {
		return ""
	}
	return strings.TrimSpace(rest[start+len("<string>") : end])
}
