package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestReadArchiveIntegrity(t *testing.T) {
	var expected source
	if err := json.Unmarshal(sourceJSON, &expected); err != nil {
		t.Fatal(err)
	}
	prefix := expected.Module + "@" + expected.Version + "/"
	files := map[string]string{"gather.go": "package ice\n", "LICENSE": "MIT\n"}
	path := filepath.Join(t.TempDir(), "module.zip")
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	var names []string
	for name, content := range files {
		names = append(names, prefix+name)
		entry, err := writer.Create(prefix + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		fmt.Fprintf(hash, "%s  %s\n", digest([]byte(files[strings.TrimPrefix(name, prefix)])), name)
	}
	expected.Sum = "h1:" + base64.StdEncoding.EncodeToString(hash.Sum(nil))
	got, err := readArchive(path, expected)
	if err != nil || string(got["gather.go"]) != files["gather.go"] {
		t.Fatalf("read verified archive: %v, %q", err, got)
	}
	expected.Sum = "h1:invalid"
	if _, err := readArchive(path, expected); err == nil {
		t.Fatal("accepted an archive with a different content checksum")
	}
}

func TestReadArchiveRejectsPaths(t *testing.T) {
	for _, name := range []string{"../escape", "dir\\escape", "", "dir/../file"} {
		t.Run(name, func(t *testing.T) {
			expected := source{Module: "example.org/ice", Version: "v1.0.0"}
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			if _, err := writer.Create(expected.Module + "@" + expected.Version + "/" + name); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "module.zip")
			if err := os.WriteFile(path, archive.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readArchive(path, expected); err == nil {
				t.Fatal("accepted an invalid archive")
			}
		})
	}
}

func TestGeneratedFilesRejectChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "gather.go")
	original := []byte("package ice\n")
	for range 2 {
		if err := writeVerified(path, original); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeVerified(path, []byte("changed")); err == nil {
		t.Fatal("overwrote generated source")
	}
	if err := checkGeneratedFiles(root, map[string][]byte{"gather.go": original}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extra.go"), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkGeneratedFiles(root, map[string][]byte{"gather.go": original}); err == nil {
		t.Fatal("accepted extra source in the patched module")
	}
}

func TestPrepareConsumer(t *testing.T) {
	// Exercise the same current-module behavior used by a downloaded tool. Keep
	// the caller's graph and shared cache intact, including under patched CI.
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	t.Chdir(t.TempDir())
	mod := []byte("module example.org/consumer\n\ngo 1.27.0\n\nrequire github.com/pion/ice/v4 v4.4.2\n")
	if err := os.WriteFile("go.mod", mod, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run("go", "mod", "download", "github.com/pion/ice/v4"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		modfile, err := prepare("out/ice-patch")
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command("go", "list", "-modfile="+modfile, "-m", "-json", "github.com/pion/ice/v4")
		raw, err := command.Output()
		if err != nil {
			t.Fatal(err)
		}
		var selected module
		if err := json.Unmarshal(raw, &selected); err != nil {
			t.Fatal(err)
		}
		if selected.Replace == nil || selected.Replace.Path != "./out/ice-patch/ice" {
			t.Fatalf("patched module not selected: %s", raw)
		}
	}
	unchanged, err := os.ReadFile("go.mod")
	if err != nil || !bytes.Equal(unchanged, mod) {
		t.Fatal("consumer go.mod was modified")
	}
	var expected source
	if err := json.Unmarshal(sourceJSON, &expected); err != nil {
		t.Fatal(err)
	}
	patched, err := os.ReadFile("out/ice-patch/ice/gather.go")
	if err != nil || digest(patched) != expected.PatchedSHA256 {
		t.Fatal("patch was not applied exactly")
	}
	if _, err := prepare("../outside"); err == nil {
		t.Fatal("accepted output outside the consumer")
	}
	if err := os.WriteFile("out/ice-patch/ice/gather.go", []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepare("out/ice-patch"); err == nil {
		t.Fatal("accepted modified patch output")
	}
	if _, err := run("go", "mod", "edit", "-replace=github.com/pion/ice/v4=./other"); err != nil {
		t.Fatal(err)
	}
	if _, err := prepare("out/other"); err == nil {
		t.Fatal("accepted an existing ICE replacement")
	}
}
