//go:build linux

package hostservice

// defaultServicesPerOS is the Linux default watchlist.
//
// `orchard.service` matches `scripts/init/orchard.service` — the
// daemon's own systemd-user unit. The `orchardist.service` /
// `contracts-tick.service` units are installed by
// `~/.claude/orchardist/scripts/orchardist` (see
// ~/.claude/orchardist/references/per-machine-startup.md). systemd is
// lenient about the `.service` suffix; it is included verbatim so the
// configured spelling matches the file on disk.
var defaultServicesPerOS = []string{
	"orchard.service",
	"orchardist.service",
	"contracts-tick.service",
}
