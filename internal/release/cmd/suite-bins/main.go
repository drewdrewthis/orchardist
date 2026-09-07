// Command suite-bins prints one suite binary set, one name per line, so the
// release scripts and the release workflow read the binary lists from
// internal/release instead of hand-mirroring them (orchardist#820).
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
	"os"
	"strings"

	"github.com/drewdrewthis/orchardist/internal/release"
)

func main() {
	valid := strings.Join(release.SetNames(), ", ")
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: suite-bins <set> (one of: %s)\n", valid)
		os.Exit(2)
	}
	set, ok := release.SetsByName[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown set %q (valid sets: %s)\n", os.Args[1], valid)
		os.Exit(2)
	}
	for _, name := range set {
		fmt.Println(name)
	}
}
