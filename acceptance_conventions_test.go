package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	knownGroupName       = "TestKnownRegressions"
	invalidSubtestName   = "literal ticket-prefixed name"
	missingSubtestMarker = "must call testutil.KnownRegression"
)

var knownSubtestName = regexp.MustCompile(`^[A-Z]+-[0-9]+_`)

func TestAcceptanceConventions(t *testing.T) {
	violations, err := checkAcceptanceTree(".")
	require.NoError(t, err)
	assert.Empty(t, violations)
}

func TestAcceptanceGroupUniqueness(t *testing.T) {
	root := t.TempDir()
	for filename, source := range map[string]string{
		"one_test.go": "package example\nfunc TestKnownRegressions(t *testing.T) {}",
		"two_test.go": "package example_test\nfunc TestKnownRegressions(t *testing.T) {}",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(root, filename), []byte(source), 0o600))
	}
	violations, err := checkAcceptanceTree(root)
	require.NoError(t, err)
	assert.Contains(t, strings.Join(violations, "\n"), "only one TestKnownRegressions per package")
}

func checkAcceptanceTree(root string) ([]string, error) {
	var violations []string
	groups := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, message := range checkAcceptanceFile(file) {
			violations = append(violations, fmt.Sprintf("%s: %s", path, message))
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || function.Name.Name != knownGroupName {
				continue
			}
			key := filepath.Join(filepath.Dir(path), strings.TrimSuffix(file.Name.Name, "_test"))
			if previous, exists := groups[key]; exists {
				violations = append(violations, fmt.Sprintf("%s and %s: only one TestKnownRegressions per package", previous, path))
			}
			groups[key] = path
		}
		return nil
	})
	return violations, err
}

func checkAcceptanceSource(filename string, source any) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), filename, source, 0)
	if err != nil {
		return nil, err
	}
	return checkAcceptanceFile(file), nil
}

func checkAcceptanceFile(file *ast.File) []string {
	var violations []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || !strings.HasPrefix(function.Name.Name, "Test") || !strings.Contains(function.Name.Name, "Regression") {
			continue
		}
		if function.Name.Name != knownGroupName {
			violations = append(violations, "must be named TestKnownRegressions")
			continue
		}
		parameters := testingParameters(function)
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Run" {
				return true
			}
			receiver, ok := selector.X.(*ast.Ident)
			if ok && parameters[receiver.Name] {
				violations = append(violations, checkKnownSubtest(call, testutilImportName(file))...)
			}
			return true
		})
	}
	return violations
}

func testingParameters(function *ast.FuncDecl) map[string]bool {
	parameters := make(map[string]bool)
	ast.Inspect(function, func(node ast.Node) bool {
		field, ok := node.(*ast.Field)
		if !ok {
			return true
		}
		pointer, ok := field.Type.(*ast.StarExpr)
		if !ok {
			return true
		}
		typeName, ok := pointer.X.(*ast.SelectorExpr)
		if ok && typeName.Sel.Name == "T" {
			for _, name := range field.Names {
				parameters[name.Name] = true
			}
		}
		return true
	})
	return parameters
}

func testutilImportName(file *ast.File) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != "github.com/coreweave/terraform-provider-coreweave/internal/testutil" {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return "testutil"
	}
	return ""
}

func checkKnownSubtest(call *ast.CallExpr, helperPackage string) []string {
	var violations []string
	if len(call.Args) != 2 {
		return []string{"expected a subtest name and callback"}
	}
	literal, ok := call.Args[0].(*ast.BasicLit)
	name := ""
	if ok && literal.Kind == token.STRING {
		name, _ = strconv.Unquote(literal.Value)
	}
	if !knownSubtestName.MatchString(name) || strings.Contains(name, "/") {
		violations = append(violations, invalidSubtestName)
	}
	callback, ok := call.Args[1].(*ast.FuncLit)
	if !ok || callback.Type.Params.NumFields() != 1 || len(callback.Type.Params.List[0].Names) != 1 {
		return append(violations, "inline subtest callback")
	}
	parameter := callback.Type.Params.List[0].Names[0].Name
	if !callsKnownMarker(callback.Body, helperPackage, parameter) {
		violations = append(violations, missingSubtestMarker)
	}
	return violations
}

func callsKnownMarker(body *ast.BlockStmt, helperPackage, parameter string) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "KnownRegression" {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		arg, isIdent := call.Args[0].(*ast.Ident)
		if ok && isIdent && pkg.Name == helperPackage && arg.Name == parameter {
			found = true
		}
		return true
	})
	return found
}

func TestAcceptanceConventionChecker(t *testing.T) {
	const imports = `package example
import "testing"
import "github.com/coreweave/terraform-provider-coreweave/internal/testutil"
`
	for _, tt := range []struct {
		name   string
		source string
		want   string
	}{
		{"ordinary test", `func TestNormal(t *testing.T) {}`, ""},
		{"unrelated run method", `func TestKnownRegressions(t *testing.T) { cmd.Run() }`, ""},
		{"non-test helper", `func helperRegression() {}`, ""},
		{"misnamed test", `func TestClusterRegression(t *testing.T) {}`, "must be named TestKnownRegressions"},
		{"valid group", `func TestKnownRegressions(t *testing.T) { t.Run("CKSAPI-753_replace", func(t *testing.T) { testutil.KnownRegression(t, "CKSAPI-753") }) }`, ""},
		{"dynamic name", `func TestKnownRegressions(t *testing.T) { t.Run(name, func(t *testing.T) { testutil.KnownRegression(t, "CKSAPI-753") }) }`, invalidSubtestName},
		{"missing ticket", `func TestKnownRegressions(t *testing.T) { t.Run("replace", func(t *testing.T) { testutil.KnownRegression(t, "CKSAPI-753") }) }`, invalidSubtestName},
		{"slash", `func TestKnownRegressions(t *testing.T) { t.Run("CKSAPI-753_replace/again", func(t *testing.T) { testutil.KnownRegression(t, "CKSAPI-753") }) }`, invalidSubtestName},
		{"missing marker", `func TestKnownRegressions(t *testing.T) { t.Run("CKSAPI-753_replace", func(t *testing.T) {}) }`, missingSubtestMarker},
		{"wrong package marker", `func TestKnownRegressions(t *testing.T) { t.Run("CKSAPI-753_replace", func(t *testing.T) { other.KnownRegression(t, "CKSAPI-753") }) }`, missingSubtestMarker},
		{"parent marker", `func TestKnownRegressions(t *testing.T) { testutil.KnownRegression(t, "CKSAPI-753"); t.Run("CKSAPI-753_replace", func(t *testing.T) {}) }`, missingSubtestMarker},
		{"wrong testing argument", `func TestKnownRegressions(t *testing.T) { t.Run("CKSAPI-753_replace", func(sub *testing.T) { testutil.KnownRegression(t, "CKSAPI-753") }) }`, missingSubtestMarker},
		{"uninvoked nested marker", `func TestKnownRegressions(t *testing.T) { t.Run("CKSAPI-753_replace", func(t *testing.T) { _ = func() { testutil.KnownRegression(t, "CKSAPI-753") } }) }`, missingSubtestMarker},
		{"nested run", `func TestKnownRegressions(t *testing.T) { t.Run("CKSAPI-753_replace", func(t *testing.T) { testutil.KnownRegression(t, "CKSAPI-753"); t.Run("bad", func(t *testing.T) {}) }) }`, invalidSubtestName},
		{"callback variable", `func TestKnownRegressions(t *testing.T) { t.Run("CKSAPI-753_replace", callback) }`, "inline subtest callback"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			violations, err := checkAcceptanceSource("fixture_test.go", imports+tt.source)
			require.NoError(t, err)
			if tt.want == "" {
				assert.Empty(t, violations)
			} else {
				assert.Contains(t, violations, tt.want)
			}
		})
	}
	_, err := checkAcceptanceSource("broken_test.go", "package broken\nfunc")
	require.Error(t, err)
}
