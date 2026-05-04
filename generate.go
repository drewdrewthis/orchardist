//go:build generate

// Package main hosts the `go generate` directive that drives schema-first
// codegen. `make generate` invokes `go generate ./...`; the line below
// runs gqlgen against schema.graphql + gqlgen.yml.
package main

//go:generate go run github.com/99designs/gqlgen generate
