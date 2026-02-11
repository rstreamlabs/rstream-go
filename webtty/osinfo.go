// See LICENSE file in the project root for license information.

package webtty

import (
	"os"
	"strconv"
	"strings"
)

type osDetails struct {
	id         string
	versionID  string
	codename   string
	prettyName string
	kernel     string
	hostname   string
}

func parseOSRelease() map[string]string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return nil
	}
	release := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if v, err := strconv.Unquote(value); err == nil {
			value = v
		}
		release[strings.TrimSpace(key)] = value
	}
	return release
}

func releaseCodename(release map[string]string) string {
	if release == nil {
		return ""
	}
	if v := release["VERSION_CODENAME"]; v != "" {
		return v
	}
	return release["UBUNTU_CODENAME"]
}

func releasePrettyFallback(release map[string]string) string {
	if release == nil {
		return ""
	}
	name := release["NAME"]
	version := release["VERSION"]
	switch {
	case name != "" && version != "":
		return name + " " + version
	case name != "":
		return name
	case version != "":
		return version
	default:
		return ""
	}
}

func productPretty(name, version string) string {
	switch {
	case name != "" && version != "":
		return name + " " + version
	case name != "":
		return name
	default:
		return version
	}
}
