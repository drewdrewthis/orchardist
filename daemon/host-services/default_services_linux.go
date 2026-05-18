//go:build linux

package hostservices

// defaultServicesPerOS is the Linux default watchlist.
var defaultServicesPerOS = []string{
	"orchard.service",
	"orchardist.service",
	"contracts-tick.service",
}
