package contracts_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const rootRepositoryModule = "github.com/asherzj/financial_configuration_center"

// This test exists only for the expand phase of the multi-module migration.
// Remove it together with the root compatibility copies after every consumer
// imports a tagged Contracts release.
func TestRootCompatibilityCopiesMatchCanonicalContracts(t *testing.T) {
	t.Parallel()

	repositoryRoot := repositoryRoot(t)
	contractsRoot := filepath.Join(repositoryRoot, "contracts")

	assertSameFile(t,
		filepath.Join(contractsRoot, "openapi/finconfig-admin-v1.yaml"),
		filepath.Join(repositoryRoot, "api/openapi/finconfig-admin-v1.yaml"),
	)

	for _, name := range []string{
		"finconfig/common/v1/common.proto",
		"finconfig/config/v1/config.proto",
		"finconfig/control/v1/control.proto",
	} {
		canonical := normalizedProto(t, filepath.Join(contractsRoot, "proto", name))
		compatibility := normalizedProto(t, filepath.Join(repositoryRoot, "api/proto", name))
		if !bytes.Equal(canonical, compatibility) {
			t.Errorf("compatibility proto %s drifted from Contracts canonical source", name)
		}
	}

	canonicalManifest := parseManifest(t, filepath.Join(contractsRoot, "schema/mysql/manifest.go"))
	compatibilityManifest := parseManifest(t, filepath.Join(repositoryRoot, "internal/platform/mysql/migrations/migrations.go"))
	if !reflect.DeepEqual(canonicalManifest, compatibilityManifest) {
		t.Errorf("root schema manifest drifted from Contracts canonical manifest: canonical=%v compatibility=%v", canonicalManifest, compatibilityManifest)
	}
}

func assertSameFile(t *testing.T, canonicalPath, compatibilityPath string) {
	t.Helper()
	canonical, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	compatibility, err := os.ReadFile(compatibilityPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, compatibility) {
		t.Errorf("compatibility file %s drifted from canonical %s", compatibilityPath, canonicalPath)
	}
}

func normalizedProto(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.ReplaceAll(
		string(content),
		rootRepositoryModule+"/contracts/gen/go/",
		rootRepositoryModule+"/gen/go/",
	))
}

type schemaManifest struct {
	Versions []int64
	Tables   []string
}

func parseManifest(t *testing.T, path string) schemaManifest {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	manifest := schemaManifest{}
	for _, declaration := range parsed.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || len(value.Values) != 1 {
				continue
			}
			literal, ok := value.Values[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			switch value.Names[0].Name {
			case "expectedVersions":
				manifest.Versions = int64Literals(t, path, literal)
			case "expectedTables":
				manifest.Tables = stringLiterals(t, path, literal)
			}
		}
	}
	if manifest.Versions == nil || manifest.Tables == nil {
		t.Fatalf("%s does not contain the complete schema manifest", path)
	}
	return manifest
}

func int64Literals(t *testing.T, path string, literal *ast.CompositeLit) []int64 {
	t.Helper()
	values := make([]int64, 0, len(literal.Elts))
	for _, element := range literal.Elts {
		basic, ok := element.(*ast.BasicLit)
		if !ok || basic.Kind != token.INT {
			t.Fatalf("%s contains a non-integer migration version", path)
		}
		value, err := strconv.ParseInt(basic.Value, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	return values
}

func stringLiterals(t *testing.T, path string, literal *ast.CompositeLit) []string {
	t.Helper()
	values := make([]string, 0, len(literal.Elts))
	for _, element := range literal.Elts {
		basic, ok := element.(*ast.BasicLit)
		if !ok || basic.Kind != token.STRING {
			t.Fatalf("%s contains a non-string table name", path)
		}
		value, err := strconv.Unquote(basic.Value)
		if err != nil {
			t.Fatal(fmt.Errorf("parse table name in %s: %w", path, err))
		}
		values = append(values, value)
	}
	return values
}
