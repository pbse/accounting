package workflows

import (
	"os"
	"path/filepath"
	"testing"

	"close.local/backend/internal/wf"
)

// A directory is a workflow: each one must register exactly the workflow named after it, and
// every registered workflow must live in its own directory.
func TestEveryDirectoryRegistersItsWorkflow(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	directories := map[string]bool{}
	for _, entry := range entries {
		if files, _ := filepath.Glob(filepath.Join(entry.Name(), "*.go")); entry.IsDir() && entry.Name() != "testdata" && len(files) > 0 {
			directories[entry.Name()] = true
		}
	}
	registered := map[string]bool{}
	for _, definition := range wf.Definitions() {
		registered[definition.Name] = true
		if !directories[definition.Name] {
			t.Errorf("workflow %q is not defined in workflows/%s", definition.Name, definition.Name)
		}
	}
	for name := range directories {
		if !registered[name] {
			t.Errorf("workflows/%s does not register a workflow named %q (run go generate ./workflows)", name, name)
		}
	}
}

// The same holds for activities: each directory under activities/ registers the activity named after it.
func TestEveryActivityDirectoryRegistersItsActivity(t *testing.T) {
	entries, err := os.ReadDir("../activities")
	if err != nil {
		t.Fatal(err)
	}
	directories := map[string]bool{}
	for _, entry := range entries {
		if files, _ := filepath.Glob(filepath.Join("../activities", entry.Name(), "*.go")); entry.IsDir() && entry.Name() != "testdata" && len(files) > 0 {
			directories[entry.Name()] = true
		}
	}
	registered := map[string]bool{}
	for _, activity := range wf.Activities() {
		registered[activity.Name] = true
		if !directories[activity.Name] {
			t.Errorf("activity %q is not defined in activities/%s", activity.Name, activity.Name)
		}
	}
	for name := range directories {
		if !registered[name] {
			t.Errorf("activities/%s does not register an activity named %q (run go generate ./workflows)", name, name)
		}
	}
}
