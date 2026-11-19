// See LICENSE file in the project root for license information.

package rundocker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
	"gopkg.in/yaml.v3"
)

func TestDocumentedDockerLabelsRespectIdentitySelection(t *testing.T) {
	guides, err := filepath.Glob(filepath.Join("..", "..", "..", "..", "docs", "*-cmd-run.md"))
	if err != nil || len(guides) != 1 {
		t.Fatalf("find command runner guide: %v, %v", guides, err)
	}
	data, err := os.ReadFile(guides[0])
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(data), "## Docker Labels")
	if !ok {
		t.Fatal("missing Docker label guide")
	}
	_, example, ok := strings.Cut(section, "```yaml\n")
	if !ok {
		t.Fatal("missing Docker label example")
	}
	example, _, ok = strings.Cut(example, "```")
	if !ok {
		t.Fatal("unterminated Docker label example")
	}
	var cfg struct {
		Labels map[string]string `yaml:"labels"`
	}
	if err := yaml.Unmarshal([]byte(example), &cfg); err != nil {
		t.Fatal(err)
	}
	for _, delegate := range []bool{false, true} {
		t.Run(map[bool]string{false: "operator context", true: "explicit delegation"}[delegate], func(t *testing.T) {
			labels := make(map[string]string, len(cfg.Labels)+1)
			for key, value := range cfg.Labels {
				labels[key] = value
			}
			if delegate {
				labels["rstream.context"] = "prod"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("API-Version", "1.48")
				if strings.HasSuffix(r.URL.Path, "/_ping") {
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode([]container.Summary{{ID: "guide", Names: []string{"/guide"}, Labels: labels, NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{"guide": {IPAddress: netip.MustParseAddr("10.0.0.2")}}}}}); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(server.Close)
			fallback := runmodel.ResolvedContext{Engine: "127.0.0.1:1", Token: "synthetic-operator-token"}
			lookups := 0
			source, err := NewSource("tcp://"+server.Listener.Addr().String(), "", fallback, func(name string) (runmodel.ResolvedContext, error) {
				lookups++
				if name != "prod" {
					t.Fatalf("unexpected context %q", name)
				}
				return runmodel.ResolvedContext{Engine: fallback.Engine, Token: "synthetic-delegated-token"}, nil
			}, delegate, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := source.Close(); err != nil {
					t.Error(err)
				}
			})
			desired, err := source.List(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			wantToken, wantLookups := fallback.Token, 0
			if delegate {
				wantToken, wantLookups = "synthetic-delegated-token", 1
			}
			if len(desired) != 1 || desired[0].Context.Token != wantToken || lookups != wantLookups {
				t.Fatalf("wrong identity selection: desired=%#v, lookups=%d", desired, lookups)
			}
		})
	}
}
