package wf

import (
	_ "embed"
	"strings"
)

//go:embed version.txt
var version string

// CodeVersion names the build that starts or runs a workflow: the git commit in deployed images
// (the deploy workflows write it to version.txt before building), "dev" otherwise.
var CodeVersion = strings.TrimSpace(version)
