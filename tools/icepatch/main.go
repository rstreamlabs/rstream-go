// Command icepatch prepares a verified local ICE patch for a module's Go builds.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed source.json
var sourceJSON []byte

//go:embed ice-urls.patch
var patch []byte

type source struct {
	Module         string `json:"module"`
	Version        string `json:"version"`
	Sum            string `json:"sum"`
	File           string `json:"file"`
	OriginalSHA256 string `json:"original_sha256"`
	PatchedSHA256  string `json:"patched_sha256"`
	PatchSHA256    string `json:"patch_sha256"`
}

type module struct {
	Path    string
	Version string
	Sum     string
	Zip     string
	Replace *module
}

func main() {
	output := flag.String("output", "out/ice-patch", "generated directory below the current module root")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: icepatch [-output directory]")
		os.Exit(2)
	}
	modfile, err := prepare(*output)
	if err != nil {
		fmt.Fprintln(os.Stderr, "icepatch:", err)
		os.Exit(1)
	}
	fmt.Println(modfile)
}

func run(name string, args ...string) ([]byte, error) {
	command := exec.Command(name, args...)
	command.Stderr = os.Stderr
	return command.Output()
}

func prepare(output string) (string, error) {
	var expected source
	if err := json.Unmarshal(sourceJSON, &expected); err != nil {
		return "", err
	}
	if digest(patch) != expected.PatchSHA256 {
		return "", errors.New("embedded patch checksum mismatch")
	}
	raw, err := run("go", "env", "-json", "GOMOD", "GOWORK", "GOFLAGS")
	if err != nil {
		return "", err
	}
	var environment map[string]string
	if err := json.Unmarshal(raw, &environment); err != nil {
		return "", err
	}
	if environment["GOWORK"] != "" && environment["GOWORK"] != "off" {
		return "", errors.New("prepare the patch with GOWORK=off for the intended module")
	}
	if strings.Contains(environment["GOFLAGS"], "-modfile") {
		return "", errors.New("prepare the patch before setting -modfile in GOFLAGS")
	}
	gomod := environment["GOMOD"]
	if gomod == "" || gomod == os.DevNull {
		return "", errors.New("run icepatch inside the consuming Go module")
	}
	root := filepath.Dir(gomod)
	output, err = filepath.Abs(output)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, output)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("output must be a generated subdirectory of the consuming module")
	}
	raw, err = run("go", "list", "-m", "-json", expected.Module)
	if err != nil {
		return "", err
	}
	var selected module
	if err := json.Unmarshal(raw, &selected); err != nil {
		return "", err
	}
	if selected.Path != expected.Module || selected.Version != expected.Version || selected.Replace != nil {
		return "", fmt.Errorf("patch requires unmodified %s@%s; review the patch before changing ICE versions or replacements", expected.Module, expected.Version)
	}
	raw, err = run("go", "mod", "download", "-json", expected.Module+"@"+expected.Version)
	if err != nil {
		return "", err
	}
	var archive module
	if err := json.Unmarshal(raw, &archive); err != nil {
		return "", err
	}
	if archive.Path != expected.Module || archive.Version != expected.Version || archive.Sum != expected.Sum {
		return "", errors.New("downloaded ICE module identity or checksum mismatch")
	}
	originalMod, err := os.ReadFile(gomod)
	if err != nil {
		return "", err
	}
	originalSum, err := os.ReadFile(strings.TrimSuffix(gomod, ".mod") + ".sum")
	if err != nil {
		return "", err
	}
	scratch, err := os.MkdirTemp("", "rstream-ice-patch-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(scratch)
	files, err := readArchive(archive.Zip, expected)
	if err != nil {
		return "", err
	}
	if digest(files[expected.File]) != expected.OriginalSHA256 {
		return "", errors.New("ICE source differs from the reviewed patch base")
	}
	if err := os.WriteFile(filepath.Join(scratch, expected.File), files[expected.File], 0600); err != nil {
		return "", err
	}
	command := exec.Command("git", "-c", "core.autocrlf=false", "apply", "--whitespace=error")
	command.Dir = scratch
	command.Env = gitEnvironment(os.Environ(), filepath.Dir(scratch))
	command.Stdin = bytes.NewReader(patch)
	if diagnostic, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("apply reviewed ICE patch: %w: %s", err, diagnostic)
	}
	patched, err := os.ReadFile(filepath.Join(scratch, expected.File))
	if err != nil {
		return "", err
	}
	if digest(patched) != expected.PatchedSHA256 {
		return "", errors.New("patched ICE source checksum mismatch")
	}
	files[expected.File] = patched
	if err := checkGeneratedFiles(filepath.Join(output, "ice"), files); err != nil {
		return "", err
	}
	for name, data := range files {
		if err := writeVerified(filepath.Join(output, "ice", filepath.FromSlash(name)), data); err != nil {
			return "", err
		}
	}
	modfile := filepath.Join(output, "go.mod")
	// Only the generated copy receives a local replacement; the source module and
	// shared module cache remain unchanged. Relative paths keep build metadata portable.
	if err := os.MkdirAll(output, 0755); err != nil {
		return "", err
	}
	stagedMod := filepath.Join(scratch, "go.mod")
	if err := os.WriteFile(stagedMod, originalMod, 0600); err != nil {
		return "", err
	}
	replacement := "./" + filepath.ToSlash(filepath.Join(relative, "ice"))
	if _, err := run("go", "mod", "edit", "-modfile="+stagedMod, "-replace="+expected.Module+"="+replacement); err != nil {
		return "", err
	}
	generatedMod, err := os.ReadFile(stagedMod)
	if err != nil {
		return "", err
	}
	// A source dependency update must regenerate the directory instead of silently
	// reusing a different graph or modifying files while another build reads them.
	if err := writeVerified(modfile, generatedMod); err != nil {
		return "", err
	}
	if err := writeVerified(filepath.Join(output, "go.sum"), originalSum); err != nil {
		return "", err
	}
	if err := writeVerified(filepath.Join(output, "provenance.json"), sourceJSON); err != nil {
		return "", err
	}
	return modfile, nil
}

func digest(data []byte) string {
	value := sha256.Sum256(data)
	return hex.EncodeToString(value[:])
}

func gitEnvironment(environment []string, ceiling string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, value := range environment {
		key, _, _ := strings.Cut(value, "=")
		if key == "GIT_DIR" || key == "GIT_WORK_TREE" || key == "GIT_INDEX_FILE" || key == "GIT_CEILING_DIRECTORIES" {
			continue
		}
		result = append(result, value)
	}
	return append(result, "GIT_CEILING_DIRECTORIES="+ceiling)
}

func readArchive(path string, expected source) (map[string][]byte, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	sort.Slice(archive.File, func(i, j int) bool { return archive.File[i].Name < archive.File[j].Name })
	hash := sha256.New()
	files := make(map[string][]byte, len(archive.File))
	prefix := expected.Module + "@" + expected.Version + "/"
	for _, entry := range archive.File {
		name, ok := strings.CutPrefix(entry.Name, prefix)
		if !ok || name == "" || strings.Contains(name, "\\") || !filepath.IsLocal(filepath.FromSlash(name)) || entry.Mode()&os.ModeSymlink != 0 || entry.FileInfo().IsDir() {
			return nil, errors.New("invalid path in ICE module archive")
		}
		if _, exists := files[name]; exists {
			return nil, errors.New("duplicate path in ICE module archive")
		}
		input, err := entry.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(input, 8<<20))
		closeErr := input.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(data) == 8<<20 {
			return nil, errors.New("ICE module entry exceeds size limit")
		}
		fmt.Fprintf(hash, "%s  %s\n", digest(data), entry.Name)
		files[name] = data
	}
	if "h1:"+base64.StdEncoding.EncodeToString(hash.Sum(nil)) != expected.Sum {
		return nil, errors.New("ICE module archive content checksum mismatch")
	}
	return files, nil
}

func checkGeneratedFiles(root string, expected map[string][]byte) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("generated ICE source must not contain symlinks: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, ok := expected[filepath.ToSlash(relative)]; !ok {
			return fmt.Errorf("unexpected file in generated ICE source: %s; remove the generated patch directory and prepare it again", path)
		}
		return nil
	})
}

func writeVerified(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("generated file must not be a symlink: %s", path)
	}
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return fmt.Errorf("generated file changed: %s; remove the generated patch directory and prepare it again", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
