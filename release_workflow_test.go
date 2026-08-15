// See LICENSE file in the project root for license information.

package rstream

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type releaseWorkflowInput struct {
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
	Type        string `yaml:"type"`
	Default     bool   `yaml:"default"`
}

type releaseWorkflowStep struct {
	Name string `yaml:"name"`
	Uses string `yaml:"uses"`
	If   string `yaml:"if"`
	With struct {
		Repository string `yaml:"repository"`
	} `yaml:"with"`
}

type releaseWorkflowJob struct {
	Steps []releaseWorkflowStep `yaml:"steps"`
}

type releaseWorkflow struct {
	On struct {
		WorkflowDispatch struct {
			Inputs map[string]releaseWorkflowInput `yaml:"inputs"`
		} `yaml:"workflow_dispatch"`
	} `yaml:"on"`
	Env  map[string]string             `yaml:"env"`
	Jobs map[string]releaseWorkflowJob `yaml:"jobs"`
}

func TestStableReleasePublicationRequiresExplicitDispatch(t *testing.T) {
	payload, err := os.ReadFile(".github/workflows/cross-compile.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	var workflow releaseWorkflow
	if err := yaml.Unmarshal(payload, &workflow); err != nil {
		t.Fatalf("parse release workflow: %v", err)
	}
	input, ok := workflow.On.WorkflowDispatch.Inputs["publish_stable"]
	if !ok || input.Type != "boolean" || !input.Required || input.Default || strings.TrimSpace(input.Description) == "" {
		t.Fatalf("publish_stable input does not fail closed: %#v", input)
	}
	gate := workflow.Env["PUBLISH_STABLE"]
	for _, fragment := range []string{"github.event_name == 'workflow_dispatch'", "github.ref_type == 'tag'", "inputs.publish_stable"} {
		if !strings.Contains(gate, fragment) {
			t.Fatalf("PUBLISH_STABLE gate %q is missing %q", gate, fragment)
		}
	}
	publishCondition := "${{ env.PUBLISH_STABLE == 'true' }}"
	requiredNames := map[string]bool{
		"Package and deploy binaries":    false,
		"Update homebrew cask file":      false,
		"Update winget manifests":        false,
		"Publish NPM package":            false,
		"Prepare MCP Registry metadata":  false,
		"Install MCP Registry publisher": false,
		"Publish MCP server metadata":    false,
	}
	requiredActions := map[string]bool{
		"docker/setup-qemu-action@":   false,
		"docker/setup-buildx-action@": false,
		"docker/login-action@":        false,
	}
	requiredRepositories := map[string]bool{
		"rstreamlabs/homebrew": false,
		"rstreamlabs/winget":   false,
		"rstreamlabs/npm":      false,
	}
	validationFound := false
	for _, step := range workflow.Jobs["build"].Steps {
		if step.Name == "Validate stable publication gate" {
			validationFound = step.If == "${{ inputs.publish_stable }}"
		}
		if _, required := requiredNames[step.Name]; required {
			requiredNames[step.Name] = step.If == publishCondition
		}
		for prefix := range requiredActions {
			if strings.HasPrefix(step.Uses, prefix) {
				requiredActions[prefix] = step.If == publishCondition
			}
		}
		if _, required := requiredRepositories[step.With.Repository]; required {
			requiredRepositories[step.With.Repository] = step.If == publishCondition
		}
	}
	if !validationFound {
		t.Fatal("stable publication input is not rejected on a branch dispatch")
	}
	for name, guarded := range requiredNames {
		if !guarded {
			t.Errorf("release mutation %q is not guarded by PUBLISH_STABLE", name)
		}
	}
	for action, guarded := range requiredActions {
		if !guarded {
			t.Errorf("release action %q is not guarded by PUBLISH_STABLE", action)
		}
	}
	for repository, guarded := range requiredRepositories {
		if !guarded {
			t.Errorf("release repository %q is not guarded by PUBLISH_STABLE", repository)
		}
	}
}
