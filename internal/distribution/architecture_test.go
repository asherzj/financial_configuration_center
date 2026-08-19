package distribution_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const repositoryModule = "github.com/asherzj/financial_configuration_center"

func TestServerDoesNotImportProductImplementations(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	forbidden := []string{
		repositoryModule + "/internal/access",
		repositoryModule + "/internal/adminbff",
		repositoryModule + "/internal/audit",
		repositoryModule + "/internal/catalog",
		repositoryModule + "/internal/outbox",
		repositoryModule + "/internal/overlay",
		repositoryModule + "/internal/platform/mysql/migrations",
		repositoryModule + "/internal/release",
		repositoryModule + "/internal/seed",
		repositoryModule + "/sdk",
	}
	for _, relative := range []string{"internal/configserver", "internal/distribution", "internal/pagequery"} {
		err := filepath.WalkDir(filepath.Join(root, relative), func(path string, entry os.DirEntry, walkErr error) error {
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
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root not found")
		}
		directory = parent
	}
}
