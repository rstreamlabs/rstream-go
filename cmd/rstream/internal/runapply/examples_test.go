// See LICENSE file in the project root for license information.

package runapply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
)

func TestDocumentedRunApplyExamples(t *testing.T) {
	t.Setenv("RSTREAM_TOKEN", "synthetic-example-token")
	t.Setenv("PROXY_USER", "fixture")
	t.Setenv("PROXY_PASS", "synthetic-proxy-password")
	root := filepath.Join("..", "..", "..", "..")
	guides, err := filepath.Glob(filepath.Join(root, "docs", "*-cmd-run.md"))
	if err != nil || len(guides) != 1 {
		t.Fatalf("find command runner guide: %v, %v", guides, err)
	}
	for _, path := range []string{filepath.Join(root, "examples", "run-yaml", "tunnels.yaml"), filepath.Join(root, "examples", "run-yaml", "README.md"), guides[0]} {
		t.Run(filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Ext(path) == ".md" {
				_, yaml, ok := strings.Cut(string(data), "```yaml\n")
				if !ok {
					t.Fatal("missing runnable YAML example")
				}
				yaml, _, ok = strings.Cut(yaml, "```")
				if !ok {
					t.Fatal("unterminated YAML example")
				}
				path = filepath.Join(t.TempDir(), "tunnels.yaml")
				if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fallback := runmodel.ResolvedContext{Engine: "127.0.0.1:1", Token: "synthetic-example-token"}
			desired, err := DesiredTunnels(path, fallback, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(desired) != 1 || desired[0].Context.Engine != fallback.Engine || desired[0].Context.Token != fallback.Token {
				t.Fatalf("example must use the selected context: %#v", desired)
			}
			props := desired[0].Props
			if props.Publish == nil || !*props.Publish || props.Protocol == nil || *props.Protocol != rstream.ProtocolHTTP || props.HTTPVersion == nil || *props.HTTPVersion != rstream.HTTP1_1 || props.UpstreamTLS == nil || *props.UpstreamTLS {
				t.Fatalf("example must publish HTTP/1.1 to a cleartext local service: %#v", props)
			}
		})
	}
}
