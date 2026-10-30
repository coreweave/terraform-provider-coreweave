package main

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	acceptanceGroupName  = "TestNonBlockingAcceptance"
	invalidSubtestName   = "literal descriptive snake_case name"
	missingSubtestMarker = "must call testutil.NonBlockingAcceptance"
)

var acceptanceSubtestName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func TestAcceptanceConventions(t *testing.T) {
	violations, err := checkAcceptanceTree(".")
	require.NoError(t, err)
	assert.Empty(t, violations)
}

func TestAcceptanceGroupUniqueness(t *testing.T) {
	root := t.TempDir()
	for filename, source := range map[string]string{
		"one_test.go": "//go:build nonblocking\n\npackage example\nfunc TestNonBlockingAcceptance(t *testing.T) {}",
		"two_test.go": "//go:build nonblocking\n\npackage example_test\nfunc TestNonBlockingAcceptance(t *testing.T) {}",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(root, filename), []byte(source), 0o600))
	}
	violations, err := checkAcceptanceTree(root)
	require.NoError(t, err)
	assert.Contains(t, strings.Join(violations, "\n"), "only one TestNonBlockingAcceptance per package")
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
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, message := range checkAcceptanceFile(file) {
			violations = append(violations, fmt.Sprintf("%s: %s", path, message))
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || function.Name.Name != acceptanceGroupName {
				continue
			}
			key := filepath.Join(filepath.Dir(path), strings.TrimSuffix(file.Name.Name, "_test"))
			if previous, exists := groups[key]; exists {
				violations = append(violations, fmt.Sprintf("%s and %s: only one TestNonBlockingAcceptance per package", previous, path))
			}
			groups[key] = path
		}
		return nil
	})
	return violations, err
}

func checkAcceptanceSource(filename string, source any) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), filename, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	return checkAcceptanceFile(file), nil
}

func checkAcceptanceFile(file *ast.File) []string {
	var violations []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || !strings.HasPrefix(function.Name.Name, "Test") || !strings.Contains(function.Name.Name, "NonBlocking") {
			continue
		}
		if function.Name.Name != acceptanceGroupName {
			violations = append(violations, "must be named TestNonBlockingAcceptance")
			continue
		}
		if !hasNonBlockingBuildConstraint(file) {
			violations = append(violations, "must use only //go:build nonblocking before the package clause")
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
				violations = append(violations, checkAcceptanceSubtest(call, testutilImportName(file))...)
			}
			return true
		})
	}
	return violations
}

func hasNonBlockingBuildConstraint(file *ast.File) bool {
	count := 0
	valid := false
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if comment.Pos() >= file.Package || !constraint.IsGoBuild(comment.Text) {
				continue
			}
			count++
			expression, err := constraint.Parse(comment.Text)
			tag, ok := expression.(*constraint.TagExpr)
			valid = err == nil && ok && tag.Tag == "nonblocking"
		}
	}
	return count == 1 && valid
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

func checkAcceptanceSubtest(call *ast.CallExpr, helperPackage string) []string {
	var violations []string
	if len(call.Args) != 2 {
		return []string{"expected a subtest name and callback"}
	}
	literal, ok := call.Args[0].(*ast.BasicLit)
	name := ""
	if ok && literal.Kind == token.STRING {
		name, _ = strconv.Unquote(literal.Value)
	}
	if !acceptanceSubtestName.MatchString(name) || strings.Contains(name, "/") {
		violations = append(violations, invalidSubtestName)
	}
	callback, ok := call.Args[1].(*ast.FuncLit)
	if !ok || callback.Type.Params.NumFields() != 1 || len(callback.Type.Params.List[0].Names) != 1 {
		return append(violations, "inline subtest callback")
	}
	parameter := callback.Type.Params.List[0].Names[0].Name
	if !callsAcceptanceMarker(callback.Body, helperPackage, parameter) {
		violations = append(violations, missingSubtestMarker)
	}
	return violations
}

func callsAcceptanceMarker(body *ast.BlockStmt, helperPackage, parameter string) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "NonBlockingAcceptance" {
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
	const imports = `//go:build nonblocking

package example
import "testing"
import "github.com/coreweave/terraform-provider-coreweave/internal/testutil"
`
	for _, tt := range []struct {
		name   string
		source string
		want   string
	}{
		{"ordinary test", `func TestNormal(t *testing.T) {}`, ""},
		{"ordinary regression test", `func TestClusterRegression(t *testing.T) {}`, ""},
		{"unrelated run method", `func TestNonBlockingAcceptance(t *testing.T) { cmd.Run() }`, ""},
		{"non-test helper", `func helperNonBlocking() {}`, ""},
		{"misnamed test", `func TestClusterNonBlocking(t *testing.T) {}`, "must be named TestNonBlockingAcceptance"},
		{"valid group", `func TestNonBlockingAcceptance(t *testing.T) { t.Run("cluster_lifecycle", func(t *testing.T) { testutil.NonBlockingAcceptance(t) }) }`, ""},
		{"dynamic name", `func TestNonBlockingAcceptance(t *testing.T) { t.Run(name, func(t *testing.T) { testutil.NonBlockingAcceptance(t) }) }`, invalidSubtestName},
		{"empty name", `func TestNonBlockingAcceptance(t *testing.T) { t.Run("", func(t *testing.T) { testutil.NonBlockingAcceptance(t) }) }`, invalidSubtestName},
		{"uppercase name", `func TestNonBlockingAcceptance(t *testing.T) { t.Run("Cluster", func(t *testing.T) { testutil.NonBlockingAcceptance(t) }) }`, invalidSubtestName},
		{"leading digit", `func TestNonBlockingAcceptance(t *testing.T) { t.Run("1_cluster", func(t *testing.T) { testutil.NonBlockingAcceptance(t) }) }`, invalidSubtestName},
		{"invalid name", `func TestNonBlockingAcceptance(t *testing.T) { t.Run("with-hyphen", func(t *testing.T) { testutil.NonBlockingAcceptance(t) }) }`, invalidSubtestName},
		{"slash", `func TestNonBlockingAcceptance(t *testing.T) { t.Run("cluster_lifecycle/again", func(t *testing.T) { testutil.NonBlockingAcceptance(t) }) }`, invalidSubtestName},
		{"missing marker", `func TestNonBlockingAcceptance(t *testing.T) { t.Run("cluster_lifecycle", func(t *testing.T) {}) }`, missingSubtestMarker},
		{"wrong package marker", `func TestNonBlockingAcceptance(t *testing.T) { t.Run("cluster_lifecycle", func(t *testing.T) { other.NonBlockingAcceptance(t) }) }`, missingSubtestMarker},
		{"parent marker", `func TestNonBlockingAcceptance(t *testing.T) { testutil.NonBlockingAcceptance(t); t.Run("cluster_lifecycle", func(t *testing.T) {}) }`, missingSubtestMarker},
		{"wrong testing argument", `func TestNonBlockingAcceptance(t *testing.T) { t.Run("cluster_lifecycle", func(sub *testing.T) { testutil.NonBlockingAcceptance(t) }) }`, missingSubtestMarker},
		{"uninvoked nested marker", `func TestNonBlockingAcceptance(t *testing.T) { t.Run("cluster_lifecycle", func(t *testing.T) { _ = func() { testutil.NonBlockingAcceptance(t) } }) }`, missingSubtestMarker},
		{"nested run", `func TestNonBlockingAcceptance(t *testing.T) { t.Run("cluster_lifecycle", func(t *testing.T) { testutil.NonBlockingAcceptance(t); t.Run("bad/name", func(t *testing.T) {}) }) }`, invalidSubtestName},
		{"callback variable", `func TestNonBlockingAcceptance(t *testing.T) { t.Run("cluster_lifecycle", callback) }`, "inline subtest callback"},
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

func TestAcceptanceBuildConstraintChecker(t *testing.T) {
	const group = "package example\nfunc TestNonBlockingAcceptance(t *testing.T) {}\n"
	for _, tt := range []struct {
		name   string
		source string
		valid  bool
	}{
		{"tagged runner", "//go:build nonblocking\n\n" + group, true},
		{"license before tag", "// Copyright example\n\n//go:build nonblocking\n\n" + group, true},
		{"missing tag", group, false},
		{"different tag", "//go:build integration\n\n" + group, false},
		{"negated tag", "//go:build !nonblocking\n\n" + group, false},
		{"alternative tag", "//go:build nonblocking || integration\n\n" + group, false},
		{"additional tag", "//go:build nonblocking && linux\n\n" + group, false},
		{"duplicate tags", "//go:build nonblocking\n//go:build nonblocking\n\n" + group, false},
		{"block comment", "/* //go:build nonblocking */\n\n" + group, false},
		{"tag after package", "package example\n//go:build nonblocking\nfunc TestNonBlockingAcceptance(t *testing.T) {}\n", false},
		{"malformed tag", "//go:build nonblocking &&\n\n" + group, false},
		{"ordinary test", "package example\nfunc TestNormal(t *testing.T) {}\n", true},
		{"untagged nonblocking body", "package example\nfunc testNonBlockingScenario(t *testing.T) {}\n", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			violations, err := checkAcceptanceSource("fixture_test.go", tt.source)
			require.NoError(t, err)
			if tt.valid {
				assert.Empty(t, violations)
			} else {
				assert.Contains(t, violations, "must use only //go:build nonblocking before the package clause")
			}
		})
	}
}

func TestAcceptanceTaggedSelection(t *testing.T) {
	root := t.TempDir()
	helper, err := os.ReadFile("internal/testutil/nonblocking_acceptance.go")
	require.NoError(t, err)
	for name, source := range map[string]string{
		"go.mod": "module github.com/coreweave/terraform-provider-coreweave\n\ngo 1.25\n",
		"original_test.go": `package example
import "testing"
func TestNormal(t *testing.T) { t.Log("normal test executed") }
func testScenario(t *testing.T) {
    t.Helper()
    t.Fatal("scenario failed")
}
`,
		"nonblocking_acceptance_test.go": `//go:build nonblocking

package example
import "testing"
import "github.com/coreweave/terraform-provider-coreweave/internal/testutil"
func TestNonBlockingAcceptance(t *testing.T) {
    t.Run("example_scenario", func(t *testing.T) {
        testutil.NonBlockingAcceptance(t)
        testScenario(t)
    })
}
`,
		"internal/testutil/nonblocking_acceptance.go": string(helper),
	} {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	}
	for _, tt := range []struct {
		name string
		args []string
		fail bool
	}{
		{"normal tests", []string{"test", "-v", "-count=1", "."}, false},
		{"non-blocking tests", []string{"test", "-v", "-count=1", "-tags=nonblocking", "-run", "^TestNonBlockingAcceptance$", "."}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), "go", tt.args...)
			command.Dir = root
			command.Env = append(os.Environ(), "TF_ACC=", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")
			output, err := command.CombinedOutput()
			if tt.fail {
				require.Error(t, err, string(output))
				assert.Contains(t, string(output), "scenario failed")
				assert.Contains(t, string(output), "acceptance_mode nonblocking")
				assert.NotContains(t, string(output), "normal test executed")
			} else {
				require.NoError(t, err, string(output))
				assert.Contains(t, string(output), "normal test executed")
				assert.NotContains(t, string(output), "TestNonBlockingAcceptance")
			}
		})
	}
}
