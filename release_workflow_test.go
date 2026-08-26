// See LICENSE file in the project root for license information.

package rstream

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type releaseWorkflowStep struct {
	Name string `yaml:"name"`
	Uses string `yaml:"uses"`
	If   string `yaml:"if"`
	With struct {
		Repository         string `yaml:"repository"`
		Ref                string `yaml:"ref"`
		Path               string `yaml:"path"`
		PersistCredentials bool   `yaml:"persist-credentials"`
	} `yaml:"with"`
}

type releaseWorkflowJob struct {
	Environment string                `yaml:"environment"`
	If          string                `yaml:"if"`
	Needs       releaseNeeds          `yaml:"needs"`
	Permissions map[string]string     `yaml:"permissions"`
	Steps       []releaseWorkflowStep `yaml:"steps"`
}

type releaseNeeds []string

func (needs *releaseNeeds) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var value string
		if err := node.Decode(&value); err != nil {
			return err
		}
		*needs = releaseNeeds{value}
		return nil
	case yaml.SequenceNode:
		var values []string
		if err := node.Decode(&values); err != nil {
			return err
		}
		*needs = values
		return nil
	default:
		return fmt.Errorf("unexpected needs node kind %d", node.Kind)
	}
}

type releaseWorkflow struct {
	Concurrency struct {
		Group string `yaml:"group"`
	} `yaml:"concurrency"`
	Jobs        map[string]releaseWorkflowJob `yaml:"jobs"`
	On          map[string]any                `yaml:"on"`
	Permissions map[string]string             `yaml:"permissions"`
}

func readReleaseWorkflow(t *testing.T, path string) releaseWorkflow {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	var workflow releaseWorkflow
	if err := yaml.Unmarshal(payload, &workflow); err != nil {
		t.Fatalf("parse release workflow: %v", err)
	}
	return workflow
}

func TestStableReleaseBuildCreatesCandidateWithoutPublishing(t *testing.T) {
	workflow := readReleaseWorkflow(t, ".github/workflows/cross-compile.yml")
	if _, ok := workflow.On["workflow_dispatch"]; ok {
		t.Fatal("release candidates must not expose a manual publication dispatch")
	}
	if workflow.Permissions["contents"] != "read" {
		t.Fatalf("candidate workflow contents permission is %q", workflow.Permissions["contents"])
	}
	build, ok := workflow.Jobs["build"]
	if !ok {
		t.Fatal("candidate workflow does not define the build job")
	}
	candidateFound := false
	nodeFound := false
	npmSourceFound := false
	uploadFound := false
	for _, step := range build.Steps {
		if step.Name == "Package stable release candidate" {
			candidateFound = step.If == "${{ github.ref_type == 'tag' }}"
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			uploadFound = step.If == "${{ github.ref_type == 'tag' }}"
		}
		if strings.HasPrefix(step.Uses, "docker/login-action@") {
			t.Fatal("candidate workflow must not log in to a container registry")
		}
		if strings.HasPrefix(step.Uses, "actions/setup-node@") {
			nodeFound = step.If == "${{ github.ref_type == 'tag' }}"
		}
		if slices.Contains([]string{"rstreamlabs/homebrew", "rstreamlabs/winget"}, step.With.Repository) {
			t.Fatalf("candidate workflow must not mutate %s", step.With.Repository)
		}
		if step.With.Repository == "rstreamlabs/npm" {
			npmSourceFound = step.If == "${{ github.ref_type == 'tag' }}" && !step.With.PersistCredentials
		}
	}
	if !candidateFound || !nodeFound || !npmSourceFound || !uploadFound {
		t.Fatalf("candidate workflow is incomplete: package=%t node=%t npm=%t upload=%t", candidateFound, nodeFound, npmSourceFound, uploadFound)
	}
	for _, step := range workflow.Jobs["build-macos"].Steps {
		if strings.HasPrefix(step.Uses, "actions/setup-node@") || step.With.Repository == "rstreamlabs/npm" {
			t.Fatal("macOS binary jobs must not prepare the npm candidate")
		}
	}
}

func TestStableReleasePromotionIsApprovedAndOrdered(t *testing.T) {
	workflow := readReleaseWorkflow(t, ".github/workflows/promote-release.yml")
	if _, ok := workflow.On["workflow_run"]; !ok {
		t.Fatal("promotion workflow is not connected to the candidate workflow")
	}
	if workflow.Concurrency.Group != "stable-release" {
		t.Fatalf("stable releases are not serialized: %q", workflow.Concurrency.Group)
	}
	if workflow.Permissions["contents"] != "read" || workflow.Permissions["actions"] != "read" {
		t.Fatalf("promotion defaults are not read-only: %#v", workflow.Permissions)
	}
	if _, ok := workflow.Permissions["id-token"]; ok {
		t.Fatal("OIDC permission must not be granted to every promotion job")
	}
	prepare, ok := workflow.Jobs["prepare"]
	if !ok {
		t.Fatal("promotion workflow does not define the approval job")
	}
	if prepare.Environment != "stable-release" {
		t.Fatalf("promotion approval environment is %q", prepare.Environment)
	}
	for _, fragment := range []string{"conclusion == 'success'", "actor.login == vars.CI_ALLOWED_ACTOR", "startsWith"} {
		if !strings.Contains(prepare.If, fragment) {
			t.Fatalf("promotion approval condition %q is missing %q", prepare.If, fragment)
		}
	}
	publishJobs := []string{
		"publish-package-api",
		"publish-debian",
		"publish-nuget",
		"publish-docker",
		"publish-homebrew",
		"publish-winget",
		"publish-npm",
		"publish-mcp",
	}
	for _, name := range publishJobs {
		job, ok := workflow.Jobs[name]
		if !ok {
			t.Errorf("promotion workflow does not define %s", name)
			continue
		}
		if !slices.Contains(job.Needs, "prepare") {
			t.Errorf("%s can run without approved candidate verification", name)
		}
	}
	for _, name := range []string{
		"publish-package-api",
		"publish-debian",
		"publish-nuget",
		"publish-docker",
		"publish-npm",
	} {
		job := workflow.Jobs[name]
		trustedCheckout := false
		trustedRestore := false
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "actions/checkout@") &&
				step.With.Ref == "${{ github.event.repository.default_branch }}" &&
				step.With.Path == ".promotion-policy" &&
				!step.With.PersistCredentials {
				trustedCheckout = true
			}
			if step.Uses == "./.promotion-policy/.github/actions/restore-release-candidate" {
				trustedRestore = true
			}
		}
		if !trustedCheckout || !trustedRestore {
			t.Errorf("%s does not restore the candidate with trusted promotion policy", name)
		}
	}
	if workflow.Jobs["publish-mcp"].Permissions["id-token"] != "write" {
		t.Fatal("MCP publication does not have its scoped OIDC permission")
	}
	finalize, ok := workflow.Jobs["finalize"]
	if !ok {
		t.Fatal("promotion workflow does not define final release publication")
	}
	if finalize.Permissions["contents"] != "write" {
		t.Fatal("only final release publication should receive contents write permission")
	}
	for _, dependency := range append([]string{"prepare"}, publishJobs...) {
		if !slices.Contains(finalize.Needs, dependency) {
			t.Errorf("GitHub release can be published without %s", dependency)
		}
	}
}

func TestReleasePleaseCreatesDraftAndTag(t *testing.T) {
	payload, err := os.ReadFile("release-please-config.json")
	if err != nil {
		t.Fatalf("read release-please config: %v", err)
	}
	var config struct {
		Packages map[string]struct {
			Draft            bool `json:"draft"`
			ForceTagCreation bool `json:"force-tag-creation"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(payload, &config); err != nil {
		t.Fatalf("parse release-please config: %v", err)
	}
	root := config.Packages["."]
	if !root.Draft || !root.ForceTagCreation {
		t.Fatalf("release-please must create a tagged draft: %#v", root)
	}
}
