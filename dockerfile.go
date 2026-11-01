// Package deck2 is what deck reads of the files at the root of its repository.
package deck2

import _ "embed"

// Dockerfile builds deck's image; its ARGs name the versions of the agent CLIs CI tests deck with.
//
//go:embed Dockerfile
var Dockerfile string
