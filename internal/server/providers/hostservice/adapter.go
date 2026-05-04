package hostservice

// NewAdapter returns the platform-specific Adapter. Build tags select
// the implementation:
//
//   - adapter_darwin.go : `launchctl list <Label>`
//   - adapter_linux.go  : `systemctl --user is-active <name>` +
//                         `systemctl --user show <name>` +
//                         `journalctl --user -u <name>`
//
// No runtime `runtime.GOOS == ...` branching outside what build tags
// already do, per the briefing's "OS-conditional via build tags" rule.

// nameToLabel is shared between the test stubs and the macOS adapter so
// the canonical "label form" (raw name as written in config) is in
// exactly one place. macOS launchd Labels are typically reverse-DNS
// (e.g. `ai.langwatch.claude-remote`), but the orchard contract treats
// the config name as the launchctl Label verbatim — operators choose
// the spelling.
func nameToLabel(name string) string { return name }
