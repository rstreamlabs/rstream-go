// See LICENSE file in the project root for license information.

package webtty

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

type UsernameVariant struct {
	Name *string
	UID  *uint32
}

func BuildEnvironment(src []*pb.Environment) []string {
	env := []string{}
	for _, e := range src {
		env = append(env, e.Key+"="+e.Value)
	}
	return env
}

func AddEnvironmentVariable(env *[]string, key, value string, force bool) {
	prefix := key + "="
	for i, kv := range *env {
		if strings.HasPrefix(kv, prefix) {
			if force {
				(*env)[i] = prefix + value
			}
			return
		}
	}
	*env = append(*env, prefix+value)
}

func DefaultLabels() map[string]string {
	info := getOSDetails()
	labels := map[string]string{
		webTTYApplicationProtocolKey: WebTTYApplicationProtocol,
	}
	set := func(k, v string) {
		if v != "" {
			labels[k] = v
		}
	}
	runtime_identity := rstream.RuntimeIdentity()
	set(webTTYOSFamilyLabel, runtime_identity.OS)
	set(webTTYArchLabel, runtime_identity.Arch)
	set(webTTYOSIDLabel, info.id)
	set(webTTYOSVersionIDLabel, info.versionID)
	set(webTTYOSVersionCodenameLabel, info.codename)
	set(webTTYOSPrettyNameLabel, info.prettyName)
	set(webTTYKernelReleaseLabel, info.kernel)
	set(webTTYHostnameLabel, info.hostname)
	return labels
}

func resolveExecutable(exe, workdir string) (string, error) {
	if strings.TrimSpace(exe) == "" {
		return "", fmt.Errorf("executable path is empty")
	}
	if workdir != "" {
		candidate := filepath.Join(workdir, exe)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			abs, err := filepath.Abs(candidate)
			if err != nil {
				return "", fmt.Errorf("resolve executable %q: %w", exe, err)
			}
			return abs, nil
		}
	}
	path, err := exec.LookPath(exe)
	if err != nil {
		return "", fmt.Errorf("executable %q not found: %w", exe, err)
	}
	return path, nil
}
