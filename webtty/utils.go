// See LICENSE file in the project root for license information.

package webtty

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"strings"

	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

type UsernameVariant struct {
	Name *string
	UID  *uint32
}

type UserInfo struct {
	Name  string
	Shell string
	Home  string
	UID   uint32
	GID   uint32
}

func GetUserInfo(u *UsernameVariant) (*UserInfo, error) {
	var usr *user.User
	var err error
	if u == nil || (u.Name == nil && u.UID == nil) {
		usr, err = user.Current()
	} else if u.Name != nil {
		usr, err = user.Lookup(*u.Name)
	} else {
		usr, err = user.LookupId(strconv.FormatUint(uint64(*u.UID), 10))
	}
	if err != nil {
		return nil, fmt.Errorf("user lookup: %w", err)
	}
	uid64, err := strconv.ParseUint(usr.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid uid %q: %w", usr.Uid, err)
	}
	gid64, err := strconv.ParseUint(usr.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid gid %q: %w", usr.Gid, err)
	}
	uid := uint32(uid64)
	gid := uint32(gid64)
	shell, err := DefaultShell(usr)
	if err != nil {
		return nil, fmt.Errorf("determine shell: %w", err)
	}
	return &UserInfo{
		Name:  usr.Username,
		Home:  usr.HomeDir,
		UID:   uid,
		GID:   gid,
		Shell: shell,
	}, nil
}

func DefaultShell(usr *user.User) (string, error) {
	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd":
		return DefaultShellUnix(usr)
	case "darwin":
		return DefaultShellDarwin(usr)
	case "windows":
		return DefaultShellWindows()
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func DefaultShellUnix(usr *user.User) (string, error) {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return "", fmt.Errorf("open /etc/passwd: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	uid := usr.Uid
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), ":", 7)
		if len(parts) == 7 && parts[2] == uid {
			return parts[6], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan /etc/passwd: %w", err)
	}
	return "", fmt.Errorf("shell for uid %s not found", uid)
}

func DefaultShellDarwin(usr *user.User) (string, error) {
	path := "/Users/" + usr.Username
	cmd := exec.Command("dscl", ".", "-read", path, "UserShell")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("dscl read %s: %w (%s)", path, err, stderr.String())
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "UserShell:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "UserShell:")), nil
		}
	}
	return "", fmt.Errorf("unexpected dscl output: %s", out)
}

func DefaultShellWindows() (string, error) {
	if s := os.Getenv("ComSpec"); s != "" {
		return s, nil
	}
	return "cmd.exe", nil
}

func BuildEnvironment(src []*pb.Environment) []string {
	env := []string{}
	for _, e := range src {
		if e.Value != "" {
			env = append(env, e.Key+"="+e.Value)
		} else if v := os.Getenv(e.Key); v != "" {
			env = append(env, e.Key+"="+v)
		}
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
