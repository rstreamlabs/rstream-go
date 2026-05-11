// See LICENSE file in the project root for license information.

package webtty

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

func TestResolveExecutablePrefersWorkdirCandidate(t *testing.T) {
	workdir := t.TempDir()
	exe := filepath.Join(workdir, "hello")
	if err := os.WriteFile(exe, []byte("echo hello\n"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	got, err := resolveExecutable("hello", workdir)
	if err != nil {
		t.Fatalf("resolveExecutable returned error: %v", err)
	}
	want, err := filepath.Abs(exe)
	if err != nil {
		t.Fatalf("filepath.Abs returned error: %v", err)
	}
	if got != want {
		t.Fatalf("unexpected executable path: got %q want %q", got, want)
	}
}

func TestResolveExecutableUsesPathLookup(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "frompath")
	if err := os.WriteFile(exe, []byte("echo hello\n"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := resolveExecutable("frompath", "")
	if err != nil {
		t.Fatalf("resolveExecutable returned error: %v", err)
	}
	if got == "" {
		t.Fatal("expected non-empty executable path")
	}
}

func TestBuildEnvironmentPreservesExplicitEmptyValues(t *testing.T) {
	t.Setenv("EMPTY", "server-value")
	env := BuildEnvironment([]*pb.Environment{{Key: "EMPTY", Value: ""}, {Key: "SET", Value: "1"}})
	if len(env) != 2 {
		t.Fatalf("unexpected env count: got %d want 2", len(env))
	}
	if env[0] != "EMPTY=" {
		t.Fatalf("unexpected first env var: got %q want %q", env[0], "EMPTY=")
	}
	if env[1] != "SET=1" {
		t.Fatalf("unexpected second env var: got %q want %q", env[1], "SET=1")
	}
}

func TestAddEnvironmentVariableForceSemantics(t *testing.T) {
	env := []string{"A=1", "B=2"}
	AddEnvironmentVariable(&env, "A", "changed", false)
	if env[0] != "A=1" {
		t.Fatalf("non-forced update should preserve existing value")
	}
	AddEnvironmentVariable(&env, "A", "changed", true)
	AddEnvironmentVariable(&env, "C", "3", false)
	if !reflect.DeepEqual(env, []string{"A=changed", "B=2", "C=3"}) {
		t.Fatalf("unexpected env: %#v", env)
	}
}

func TestDefaultLabelsIncludesStableRuntimeIdentity(t *testing.T) {
	labels := DefaultLabels()
	if labels[webTTYApplicationProtocolKey] != WebTTYApplicationProtocol {
		t.Fatalf("protocol label = %q, want %q", labels[webTTYApplicationProtocolKey], WebTTYApplicationProtocol)
	}
	if labels[webTTYOSFamilyLabel] == "" || labels[webTTYArchLabel] == "" {
		t.Fatalf("runtime labels missing OS/arch: %#v", labels)
	}
	for key, value := range labels {
		if value == "" {
			t.Fatalf("label %q has an empty value", key)
		}
	}
}
