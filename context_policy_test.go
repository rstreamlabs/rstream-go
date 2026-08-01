// See LICENSE file in the project root for license information.

package rstream

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type backgroundContextUse struct {
	count  int
	reason string
}

var allowedBackgroundContexts = map[string]backgroundContextUse{
	"config/client.go:NewClientFromEnvOptions":          {count: 1, reason: "the public convenience wrapper intentionally starts a client construction operation"},
	"webtty/client_session.go:ClientSession.SendInput":  {count: 1, reason: "the public convenience wrapper delegates to the context-aware input API"},
	"webtty/client_session.go:decodeClientSessionEvent": {count: 1, reason: "the helper decodes an in-memory protocol message without I/O"},
}

func TestProductionContextPolicy(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	actual := make(map[string]int)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if contextPolicyIgnoredDirectory(relative) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".pb.go") {
			return nil
		}
		return inspectContextPolicyFile(path, filepath.ToSlash(relative), actual)
	})
	if err != nil {
		t.Fatalf("context policy scan error = %v", err)
	}
	var violations []string
	for key, count := range actual {
		allowed, ok := allowedBackgroundContexts[key]
		if !ok {
			violations = append(violations, key+" uses context.Background directly")
			continue
		}
		if allowed.count != count {
			violations = append(violations, key+" direct context.Background count changed")
		}
		if strings.TrimSpace(allowed.reason) == "" {
			violations = append(violations, key+" has no justification")
		}
	}
	for key := range allowedBackgroundContexts {
		if actual[key] == 0 {
			violations = append(violations, key+" no longer uses context.Background")
		}
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("production context policy violations:\n%s", strings.Join(violations, "\n"))
	}
}

func contextPolicyIgnoredDirectory(path string) bool {
	switch filepath.ToSlash(path) {
	case ".claude", ".git", "examples", "out", "test", "vendor":
		return true
	default:
		return false
	}
}

func inspectContextPolicyFile(path, relative string, actual map[string]int) error {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		return err
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		name := function.Name.Name
		if function.Recv != nil && len(function.Recv.List) > 0 {
			name = receiverName(function.Recv.List[0].Type) + "." + name
		}
		inspectContextPolicyFunction(relative+":"+name, function.Body, actual)
	}
	return nil
}

func inspectContextPolicyFunction(key string, body *ast.BlockStmt, actual map[string]int) {
	var stack []ast.Node
	ast.Inspect(body, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if call, kind, ok := contextConstructor(node); ok {
			if kind == "TODO" {
				actual[key+":context.TODO"]++
			} else if !boundedBackgroundParent(call, stack) {
				actual[key]++
			}
		}
		stack = append(stack, node)
		return true
	})
}

func contextConstructor(node ast.Node) (*ast.CallExpr, string, bool) {
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return nil, "", false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, "", false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || pkg.Name != "context" || selector.Sel.Name != "Background" && selector.Sel.Name != "TODO" {
		return nil, "", false
	}
	return call, selector.Sel.Name, true
}

func boundedBackgroundParent(call *ast.CallExpr, stack []ast.Node) bool {
	if len(stack) == 0 {
		return false
	}
	parent, ok := stack[len(stack)-1].(*ast.CallExpr)
	if !ok {
		return false
	}
	for _, argument := range parent.Args {
		if argument != call {
			continue
		}
		selector, ok := parent.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok {
			return false
		}
		return pkg.Name == "context" && (selector.Sel.Name == "WithCancel" || selector.Sel.Name == "WithCancelCause" || selector.Sel.Name == "WithDeadline" || selector.Sel.Name == "WithTimeout") || pkg.Name == "signal" && selector.Sel.Name == "NotifyContext"
	}
	return false
}

func receiverName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return receiverName(value.X)
	case *ast.IndexExpr:
		return receiverName(value.X)
	case *ast.IndexListExpr:
		return receiverName(value.X)
	default:
		return "receiver"
	}
}
