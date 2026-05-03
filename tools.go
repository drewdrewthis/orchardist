//go:build tools

// Package tools pins build-time tool dependencies (gqlgen) so they are
// tracked in go.mod / go.sum. The build tag keeps these out of normal
// builds; `go generate ./...` invokes them directly via go run.
//
// See https://gqlgen.com/recipes/gqlgen-with-modules/ for the standard
// pattern.
package tools

import (
	_ "github.com/99designs/gqlgen"
	_ "github.com/99designs/gqlgen/graphql/introspection"
)
