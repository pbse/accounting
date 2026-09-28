// Package workflows registers every workflow: each directory here is one workflow, whose package
// calls wf.Define with a Name equal to the directory name. Import this package for its side
// effects (cmd/worker does). See docs/workflow-engine.md.
package workflows

//go:generate go run ../internal/wf/gen
