// Command suite-bins prints one suite binary set, one name per line, so the
// release scripts and the release workflow read the binary lists from
// internal/release instead of hand-mirroring them (orchardist#820).
//
// It lives under internal/release/cmd/, not top-level cmd/: it is a dev/CI
// tool that reads the suite roster, not a binary the suite ships, so
// top-level cmd/* stays exactly the set of shipped binaries.
//
// Usage:
//
//	go run ./internal/release/cmd/suite-bins <set>
//
// where <set> is one of suite, go, rust, revision. The names print in
// SuiteBinaries order. An unknown or missing set writes an error naming the
// valid sets to stderr, prints nothing to stdout, and exits 2.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/drewdrewthis/orchardist/internal/release"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run holds all the command's logic so a test can drive it directly with
// captured stdout/stderr instead of subprocessing the built binary.
func run(args []string, stdout, stderr io.Writer) int {
	valid := strings.Join(release.SetNames(), ", ")
	if len(args) != 1 {
		fmt.Fprintf(stderr, "usage: suite-bins <set> (one of: %s)\n", valid)
		return 2
	}
	set, ok := release.SetsByName[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "unknown set %q (valid sets: %s)\n", args[0], valid)
		return 2
	}
	for _, name := range set {
		fmt.Fprintln(stdout, name)
	}
	return 0
}
