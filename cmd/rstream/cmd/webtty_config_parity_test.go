// See LICENSE file in the project root for license information.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

func yamlLeafPaths(typ reflect.Type, prefix string) map[string]bool {
	paths := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		path := prefix + name
		child := field.Type
		if child.Kind() == reflect.Pointer {
			child = child.Elem()
		}
		if child.Kind() == reflect.Struct {
			for nested := range yamlLeafPaths(child, path+".") {
				paths[nested] = true
			}
		} else {
			paths[path] = true
		}
	}
	return paths
}

func TestWebTTYRuntimeOptionsCoverCLIAndYAML(t *testing.T) {
	cfg := &webTTYServerRuntimeConfig{}
	bindings := webTTYBindings(cfg)
	paths := yamlLeafPaths(reflect.TypeOf(*cfg), "")
	delete(paths, "version")
	flags := map[string]bool{"webtty-config": true, "no-publish": true, "no-retry": true}
	check := func(flag, path string) {
		if flags[flag] {
			t.Errorf("duplicate flag %s", flag)
		}
		flags[flag] = true
		if !paths[path] {
			t.Errorf("unknown or duplicate YAML path %s", path)
		}
		delete(paths, path)
	}
	for _, b := range bindings.strings {
		check(b.flag, b.path)
	}
	for _, b := range bindings.bools {
		check(b.flag, b.path)
	}
	for _, b := range bindings.integers {
		check(b.flag, b.path)
	}
	for _, b := range bindings.arrays {
		check(b.flag, b.path)
	}
	check(bindings.labels.flag, bindings.labels.path)
	if len(paths) > 0 {
		t.Errorf("YAML fields without CLI mapping: %v", paths)
	}
	cmd := newTestWebTTYServerCommand()
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if !flags[flag.Name] {
			t.Errorf("CLI flag without YAML mapping or documented exclusion: %s", flag.Name)
		}
		delete(flags, flag.Name)
	})
	if len(flags) > 0 {
		t.Errorf("mappings without registered flags: %v", flags)
	}
}

func TestWebTTYEveryRuntimeBindingHasEquivalentInputs(t *testing.T) {
	bindings := webTTYBindings(&webTTYServerRuntimeConfig{})
	check := func(flag, path, cliValue string, yamlValue any) {
		t.Helper()
		t.Run(flag+"/"+cliValue, func(t *testing.T) {
			mapping := map[string]any{}
			current := mapping
			parts := strings.Split(path, ".")
			for _, part := range parts[:len(parts)-1] {
				next := map[string]any{}
				current[part] = next
				current = next
			}
			current[parts[len(parts)-1]] = yamlValue
			data, err := yaml.Marshal(mapping)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := loadWebTTYServerRuntimeConfig(writeWebTTYRuntimeConfigFixture(t, string(data)))
			if err != nil {
				t.Fatal(err)
			}
			fromFile, fromCLI := newTestWebTTYServerCommand(), newTestWebTTYServerCommand()
			if err := fromCLI.Flags().Set(flag, cliValue); err != nil {
				t.Fatal(err)
			}
			if err := applyWebTTYServerRuntimeConfigValues(fromFile, cfg); err != nil {
				t.Fatal(err)
			}
			if err := applyWebTTYServerRuntimeConfigValues(fromCLI, nil); err != nil {
				t.Fatal(err)
			}
			fromCLI.Flags().VisitAll(func(f *pflag.Flag) {
				other := fromFile.Flags().Lookup(f.Name)
				if f.Changed != other.Changed || f.Value.String() != other.Value.String() {
					t.Errorf("%s differs: CLI=%s (%t), YAML=%s (%t)", f.Name, f.Value, f.Changed, other.Value, other.Changed)
				}
			})
			if !fromFile.Flags().Changed(flag) {
				t.Errorf("explicit %s was discarded", path)
			}
		})
	}
	for _, b := range bindings.strings {
		check(b.flag, b.path, "sample", "sample")
		check(b.flag, b.path, "", "")
	}
	for _, b := range bindings.bools {
		for _, value := range []bool{false, true} {
			check(b.flag, b.path, fmt.Sprint(value), value)
		}
	}
	for _, b := range bindings.integers {
		for _, value := range []int64{0, 1200} {
			check(b.flag, b.path, fmt.Sprint(value), value)
		}
	}
	for _, b := range bindings.arrays {
		check(b.flag, b.path, " sample ", []string{" sample "})
		check(b.flag, b.path, "", []string{})
	}
	check("label", "server.labels", " env = production ", map[string]string{" env ": " production "})
}

func TestWebTTYRuntimeExplicitOverrides(t *testing.T) {
	cases := []struct {
		name, yaml                      string
		flags                           []string
		wantFlag, wantValue, absentFlag string
	}{
		{"identity selector", "e2e:\n  identityFile: /configured/identity.json", []string{"--identity=chosen"}, "identity", "chosen", "identity-file"},
		{"identity file selector", "e2e:\n  identity: configured", []string{"--identity-file=/chosen/identity.json"}, "identity-file", "/chosen/identity.json", "identity"},
		{"enrollment selector", "server:\n  serverEnrollment: /configured/enrollment.yaml", []string{"--server-id=chosen"}, "server-id", "chosen", "server-enrollment"},
		{"false", "server:\n  publish: true", []string{"--publish=false"}, "publish", "false", ""},
		{"inverse false", "server:\n  publish: false", []string{"--no-publish=false"}, "publish", "true", "no-publish"},
		{"empty", "server:\n  host: configured.example.com", []string{"--host="}, "host", "", ""},
		{"zero", "server:\n  shutdownTimeoutMs: 1200", []string{"--shutdown-timeout=0"}, "shutdown-timeout", "0", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadWebTTYServerRuntimeConfig(writeWebTTYRuntimeConfigFixture(t, tc.yaml))
			if err != nil {
				t.Fatal(err)
			}
			cmd := newTestWebTTYServerCommand()
			if err := cmd.ParseFlags(tc.flags); err != nil {
				t.Fatal(err)
			}
			if err := applyWebTTYServerRuntimeConfigValues(cmd, cfg); err != nil {
				t.Fatal(err)
			}
			if got := cmd.Flags().Lookup(tc.wantFlag).Value.String(); got != tc.wantValue {
				t.Fatalf("got %q, want %q", got, tc.wantValue)
			}
			if tc.absentFlag != "" && flagChanged(cmd, tc.absentFlag) {
				t.Errorf("overridden %s remains present", tc.absentFlag)
			}
		})
	}
}

func TestWebTTYRuntimeRejectsConflictingAndMalformedInputs(t *testing.T) {
	for _, args := range [][]string{{"--publish", "--no-publish"}, {"--retry", "--no-retry"}, {"--label=invalid"}, {"--label==value"}, {"--label=env=one", "--label= env =two"}} {
		cmd := newTestWebTTYServerCommand()
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatal(err)
		}
		if err := applyWebTTYServerRuntimeConfigValues(cmd, nil); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	for _, contents := range []string{"server:\n  labels:\n    env: one\n    ' env ': two", "server:\n  labels:\n    ' ': value"} {
		cfg, err := loadWebTTYServerRuntimeConfig(writeWebTTYRuntimeConfigFixture(t, contents))
		if err != nil {
			t.Fatal(err)
		}
		if err := applyWebTTYServerRuntimeConfigValues(newTestWebTTYServerCommand(), cfg); err == nil {
			t.Errorf("accepted malformed labels: %s", contents)
		}
	}
}

func TestWebTTYHostAndBackendEquivalentRuntimeBehavior(t *testing.T) {
	for _, source := range []string{"cli", "yaml"} {
		t.Run(source, func(t *testing.T) {
			cmd := newTestWebTTYServerCommand()
			var cfg *webTTYServerRuntimeConfig
			if source == "cli" {
				if err := cmd.ParseFlags([]string{"--rstream", "--transport=websocket", "--host=shell.example.com", "--fs-root=/tmp", "--fs-backend=webrtc"}); err != nil {
					t.Fatal(err)
				}
			} else {
				var err error
				cfg, err = loadWebTTYServerRuntimeConfig(writeWebTTYRuntimeConfigFixture(t, "server:\n  rstream: true\n  transport: websocket\n  host: shell.example.com\nfilesystem:\n  root: /tmp\n  backend: webrtc"))
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := applyWebTTYServerRuntimeConfigValues(cmd, cfg); err != nil {
				t.Fatal(err)
			}
			if err := applyWebTTYServerDerivedDefaults(cmd); err != nil {
				t.Fatal(err)
			}
			if err := validateWebTTYServerFlags(cmd); err != nil {
				t.Fatal(err)
			}
			props := newWebTTYServerTunnelProperties(cmd, nil)
			if props.Hostname == nil || *props.Hostname != "shell.example.com" || props.Publish == nil || !*props.Publish {
				t.Fatalf("incorrect public endpoint: %#v", props)
			}
			if backend, _ := cmd.Flags().GetString("fs-backend"); backend != "webrtc" {
				t.Fatalf("backend=%s", backend)
			}
		})
	}
	for _, args := range [][]string{{"--host=shell.example.com"}, {"--rstream", "--host="}, {"--rstream", "--host=shell.example.com", "--publish=false"}, {"--rstream", "--host=shell.example.com", "--transport=plain"}, {"--server-id=test", "--login-user=test", "--transport=webtransport", "--publish=false"}} {
		cmd := newTestWebTTYServerCommand()
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatal(err)
		}
		if err := applyWebTTYServerRuntimeConfigValues(cmd, nil); err != nil {
			t.Fatal(err)
		}
		if err := validateWebTTYServerFlags(cmd); err == nil {
			t.Errorf("accepted invalid endpoint: %v", args)
		}
	}
}

func TestWebTTYPublicationAndStableHostnameAcrossReconnects(t *testing.T) {
	for _, args := range [][]string{{"--publish=false"}, {"--no-publish"}} {
		cmd := newTestWebTTYServerCommand()
		if err := cmd.ParseFlags(append([]string{"--rstream", "--transport=websocket"}, args...)); err != nil {
			t.Fatal(err)
		}
		props := newWebTTYServerTunnelProperties(cmd, nil)
		if props.Publish == nil || *props.Publish {
			t.Errorf("private flag ignored: %v", args)
		}
	}
	for _, hostname := range []string{"shell.example.com", ""} {
		cmd := newTestWebTTYServerCommand()
		args := []string{"--rstream", "--transport=websocket"}
		if hostname != "" {
			args = append(args, "--host="+hostname)
		}
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatal(err)
		}
		var stable *string
		for attempt := 0; attempt < 3; attempt++ {
			props := newWebTTYServerTunnelProperties(cmd, nil)
			if err := applyWebTTYStableHostname(&props, "https://engine.example.com", &stable); err != nil {
				t.Fatal(err)
			}
			if stable == nil || props.Hostname == nil || *props.Hostname != *stable {
				t.Fatal("hostname did not survive reconnect")
			}
			if hostname == "" {
				hostname = *props.Hostname
			}
			if *props.Hostname != hostname {
				t.Fatalf("hostname changed: %q, want %q", *props.Hostname, hostname)
			}
		}
	}
}

func TestWebTTYOwnedYAMLRejectsAdditionalDocuments(t *testing.T) {
	for _, suffix := range []string{"\n---\n", "\n---\nserver:\n  publish: true\n"} {
		path := writeWebTTYRuntimeConfigFixture(t, "version: 1\n"+suffix)
		if _, err := loadWebTTYServerRuntimeConfig(path); err == nil || !strings.Contains(err.Error(), "exactly one YAML document") {
			t.Fatalf("runtime: %v", err)
		}
		enrollmentPath := filepath.Join(t.TempDir(), "enrollment.yaml")
		if err := os.WriteFile(enrollmentPath, []byte("version: 1\n"+suffix), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadWebTTYServerEnrollmentFile(enrollmentPath); err == nil || !strings.Contains(err.Error(), "exactly one YAML document") {
			t.Fatalf("enrollment: %v", err)
		}
	}
}

func TestWebTTYExplicitLocalModeHasSameValidationInBothSources(t *testing.T) {
	for _, source := range []string{"cli", "yaml"} {
		cmd := newTestWebTTYServerCommand()
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			if err := applyWebTTYServerRuntimeConfig(cmd); err != nil {
				return err
			}
			return validateWebTTYServerFlags(cmd)
		}
		args := []string{"--allow-unauthenticated", "--transport=websocket"}
		if source == "cli" {
			args = append(args, "--rstream=false", "--listen=127.0.0.1:0")
		} else {
			path := writeWebTTYRuntimeConfigFixture(t, "server:\n  rstream: false\n  listen: 127.0.0.1:0")
			args = append(args, "--webtty-config", path)
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
}
