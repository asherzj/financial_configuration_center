package contracts_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const repositoryModule = "github.com/asherzj/financial_configuration_center"

func TestContractSourcesAndGeneratedBindingsExist(t *testing.T) {
	t.Parallel()

	required := []string{
		"proto/finconfig/common/v1/common.proto",
		"proto/finconfig/config/v1/config.proto",
		"proto/finconfig/control/v1/control.proto",
		"openapi/finconfig-admin-v1.yaml",
		"gen/go/finconfig/config/v1/config_grpc.pb.go",
		"kitex_gen/finconfig/config/v1/configservice/client.go",
		"schema/mysql/manifest.go",
		"buf.yaml",
		"buf.gen.yaml",
	}

	root := moduleRoot(t)
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("required contract source %s: %v", name, err)
		}
	}
}

func TestProtoSourcesUseContractsOwnedGoPackages(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	for _, name := range []string{
		"proto/finconfig/common/v1/common.proto",
		"proto/finconfig/config/v1/config.proto",
		"proto/finconfig/control/v1/control.proto",
	} {
		assertContains(t, filepath.Join(root, name),
			"option go_package = \""+repositoryModule+"/contracts/gen/go/")
	}
	assertContains(t, filepath.Join(root, "proto/finconfig/common/v1/common.proto"),
		"server_epoch", "server_instance_id", "snapshot_generation")
	assertContains(t, filepath.Join(root, "proto/finconfig/control/v1/control.proto"),
		"expected_record_revision", "expected_collection_revision", "expected_order_revision", "action_request_id")
}

func TestAdminOpenAPIIsSelfContainedAndHasUniqueOperations(t *testing.T) {
	t.Parallel()

	path := filepath.Join(moduleRoot(t), "openapi/finconfig-admin-v1.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(b, &document); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	if got := document["openapi"]; got != "3.1.0" {
		t.Fatalf("openapi version = %v, want 3.1.0", got)
	}

	operationIDs := make(map[string]string)
	walkDocument(document, func(location string, value any) {
		object, ok := value.(map[string]any)
		if !ok {
			return
		}
		if operationID, ok := object["operationId"].(string); ok {
			if previous, duplicate := operationIDs[operationID]; duplicate {
				t.Errorf("duplicate operationId %q at %s and %s", operationID, previous, location)
			}
			operationIDs[operationID] = location
		}
		if ref, ok := object["$ref"].(string); ok {
			if !strings.HasPrefix(ref, "#/components/") {
				t.Errorf("external OpenAPI ref %q at %s", ref, location)
				return
			}
			if !openAPIRefExists(document, ref) {
				t.Errorf("unresolved OpenAPI ref %q at %s", ref, location)
			}
		}
	})
	if len(operationIDs) < 15 {
		t.Fatalf("operation count = %d, want at least 15", len(operationIDs))
	}
	for route := range document["paths"].(map[string]any) {
		if strings.Contains(route, "/records") {
			t.Errorf("direct ConfigurationRecord route is forbidden: %s", route)
		}
	}
}

func TestContractsModuleDoesNotImportProductImplementations(t *testing.T) {
	t.Parallel()

	forbidden := []string{
		repositoryModule + "/admin",
		repositoryModule + "/server",
		repositoryModule + "/client_sdk",
		repositoryModule + "/platform",
		repositoryModule + "/internal",
	}
	err := filepath.WalkDir(moduleRoot(t), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range parsed.Imports {
			value, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			for _, prefix := range forbidden {
				if value == prefix || strings.HasPrefix(value, prefix+"/") {
					t.Errorf("%s imports forbidden product implementation %s", path, value)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func walkDocument(value any, visit func(string, any)) {
	var walk func(string, any)
	walk = func(location string, current any) {
		visit(location, current)
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				walk(location+"/"+key, child)
			}
		case []any:
			for index, child := range typed {
				walk(location+"/"+strconv.Itoa(index), child)
			}
		}
	}
	walk("#", value)
}

func openAPIRefExists(document map[string]any, ref string) bool {
	var current any = document
	for _, segment := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current, ok = object[segment]
		if !ok {
			return false
		}
	}
	return current != nil
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("contracts module root not found")
		}
		dir = parent
	}
}

func assertContains(t *testing.T, path string, needles ...string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("read %s: %v", path, err)
		return
	}
	for _, needle := range needles {
		if !strings.Contains(string(b), needle) {
			t.Errorf("%s does not contain %q", path, needle)
		}
	}
}
