package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPackagesListsCodeDirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	for dir, file := range map[string]string{"month_kickoff": "workflow.go", "notes": "README.md", "testdata": "x.go", "_draft": "x.go"} {
		must(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
		must(t, os.WriteFile(filepath.Join(root, dir, file), []byte("package x\n"), 0o644))
	}
	names, err := packages(root)
	must(t, err)
	if !slices.Equal(names, []string{"month_kickoff"}) {
		t.Fatalf("got %v", names)
	}
	must(t, os.MkdirAll(filepath.Join(root, "Bad-Name"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "Bad-Name", "x.go"), []byte("package x\n"), 0o644))
	if _, err = packages(root); err == nil {
		t.Fatal("a directory that cannot be a workflow name must be rejected")
	}
	if names, err = packages(filepath.Join(root, "missing")); err != nil || names != nil {
		t.Fatalf("a missing tree lists nothing, got %v %v", names, err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
