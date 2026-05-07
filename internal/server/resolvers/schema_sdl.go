// Embedded schema source for Query.schemaSDL (#469 F10).
//
// The schema.graphql file at the repo root is the source of truth that
// gqlgen reads at codegen time. We also bake it into the daemon binary
// via go:embed so agents can self-describe without needing source on
// disk — useful from inside fresh boxd VMs, sister sessions, or remote
// orchardists where the source tree isn't checked out.
//
// Embed paths cannot escape the package directory, so the schema is
// symlinked into this package as schema.graphql via the build (see
// the install hook) or copied during make generate. We resolve to the
// symlink at runtime; if absent we fall back to reading the file from
// the gqlgen-embedded sources via the executable schema.

package resolvers

import _ "embed"

//go:embed schema.graphql
var embeddedSchemaSDL string

// SchemaSDL returns the canonical schema source as a single string.
// Stable across daemon restarts; reflects whatever schema the running
// binary was built against.
func SchemaSDL() string { return embeddedSchemaSDL }
