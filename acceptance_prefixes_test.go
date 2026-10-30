package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type prefixOverlap struct {
	ResourceType              string
	First, Second             string
	FirstPrefix, SecondPrefix string
}

func acceptanceSweeperPrefixes(t *testing.T) map[string]map[string]string {
	t.Helper()
	files, err := filepath.Glob("coreweave/*/*_test.go")
	require.NoError(t, err)
	prefixes := make(map[string]string)
	for _, filename := range files {
		file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
		require.NoError(t, err)
		for _, declaration := range file.Decls {
			constants, ok := declaration.(*ast.GenDecl)
			if !ok || constants.Tok != token.CONST {
				continue
			}
			for _, spec := range constants.Specs {
				value, ok := spec.(*ast.ValueSpec)
				require.True(t, ok)
				for index, name := range value.Names {
					if name.Name != "AcceptanceTestPrefix" && name.Name != "cksVPCNamePrefix" {
						continue
					}
					require.Greater(t, len(value.Values), index, "%s: prefix must have a literal value", filename)
					literal, ok := value.Values[index].(*ast.BasicLit)
					require.True(t, ok, "%s: prefix must be a string literal", filename)
					prefix, err := strconv.Unquote(literal.Value)
					require.NoError(t, err)
					require.NotEmpty(t, prefix)
					key := filepath.Base(filepath.Dir(filename)) + "/" + name.Name
					require.NotContains(t, prefixes, key)
					prefixes[key] = prefix
				}
			}
		}
	}

	// Registry prefixes are runtime configuration. Check the actual CI default;
	// this cannot prove that a local or repository-variable override is disjoint.
	workflow, err := os.ReadFile(".github/workflows/acceptance-test.yaml")
	require.NoError(t, err)
	matches := regexp.MustCompile(`vars\.COREWEAVE_CONTAINER_REGISTRY_TEST_PREFIX \|\| '([^']+)'`).FindAllStringSubmatch(string(workflow), -1)
	require.NotEmpty(t, matches, "registry CI default must be included in the prefix check")
	for _, match := range matches {
		require.Equal(t, matches[0][1], match[1], "all jobs must use the same registry prefix default")
	}
	prefixes["container_registry/CI default"] = matches[0][1]
	// Both CKS and networking sweep the same VPC API despite different sweeper names.
	// caller_identity only registers TestMain; it has no resource sweeper.
	resourceTypes := map[string][]string{
		"cks/AcceptanceTestPrefix":        {"coreweave_cks_cluster"},
		"cks/cksVPCNamePrefix":            {"coreweave_networking_vpc"},
		"networking/AcceptanceTestPrefix": {"coreweave_networking_vpc"},
		"inference/AcceptanceTestPrefix": {
			"coreweave_inference_deployment", "coreweave_inference_capacity_claim", "coreweave_inference_gateway",
		},
		"object_storage/AcceptanceTestPrefix": {
			"coreweave_object_storage_bucket", "coreweave_object_storage_organization_access_policy",
		},
		"container_registry/CI default": {"coreweave_container_registry_namespace"},
	}
	require.Equal(t, slices.Sorted(maps.Keys(resourceTypes)), slices.Sorted(maps.Keys(prefixes)), "update the resource type inventory when adding a suite prefix")
	grouped := make(map[string]map[string]string)
	for source, prefix := range prefixes {
		for _, resourceType := range resourceTypes[source] {
			if grouped[resourceType] == nil {
				grouped[resourceType] = make(map[string]string)
			}
			grouped[resourceType][source] = prefix
		}
	}
	return grouped
}

func overlappingPrefixes(groups map[string]map[string]string) []prefixOverlap {
	var overlaps []prefixOverlap
	for _, resourceType := range slices.Sorted(maps.Keys(groups)) {
		prefixes := groups[resourceType]
		names := slices.Sorted(maps.Keys(prefixes))
		for index, first := range names {
			for _, second := range names[index+1:] {
				if strings.HasPrefix(prefixes[first], prefixes[second]) || strings.HasPrefix(prefixes[second], prefixes[first]) {
					overlaps = append(overlaps, prefixOverlap{resourceType, first, second, prefixes[first], prefixes[second]})
				}
			}
		}
	}
	return overlaps
}

func TestAcceptanceSweeperPrefixes(t *testing.T) {
	assert.Empty(t, overlappingPrefixes(acceptanceSweeperPrefixes(t)), "prefixes for the same swept resource type must be distinct and non-overlapping")
}

func TestSweeperPrefixChecker(t *testing.T) {
	const (
		basePrefix    = "test-"
		childPrefix   = "test-a-"
		siblingPrefix = "test-b-"
		clusterType   = "cluster"
		vpcType       = "vpc"
	)
	for _, tt := range []struct {
		name     string
		prefixes map[string]map[string]string
		want     []prefixOverlap
	}{
		{"distinct", map[string]map[string]string{vpcType: {"a": childPrefix, "b": siblingPrefix}}, nil},
		{"identical", map[string]map[string]string{vpcType: {"a": basePrefix, "b": basePrefix}}, []prefixOverlap{{vpcType, "a", "b", basePrefix, basePrefix}}},
		{"same type enclosing", map[string]map[string]string{vpcType: {"a": basePrefix, "b": siblingPrefix}}, []prefixOverlap{{vpcType, "a", "b", basePrefix, siblingPrefix}}},
		{"reverse enclosing", map[string]map[string]string{vpcType: {"a": childPrefix, "b": basePrefix}}, []prefixOverlap{{vpcType, "a", "b", childPrefix, basePrefix}}},
		{"single", map[string]map[string]string{vpcType: {"a": basePrefix}}, nil},
		{"different type enclosing", map[string]map[string]string{clusterType: {"a": basePrefix}, vpcType: {"b": childPrefix}}, nil},
		{"mixed types preserve same type overlap", map[string]map[string]string{clusterType: {"a": basePrefix}, vpcType: {"b": childPrefix, "c": basePrefix}}, []prefixOverlap{{vpcType, "b", "c", childPrefix, basePrefix}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, overlappingPrefixes(tt.prefixes))
		})
	}
}
