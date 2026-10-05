package ps

import "runtime"

// osGOOS exposes runtime.GOOS through a package-level seam. Centralised
// here so tests and the cwd path agree on the same value.
func osGOOS() string { return runtime.GOOS }
