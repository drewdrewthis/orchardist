// Package ps implements the orchard ps provider per ADR-011 §5.1.
//
// It surfaces the Process node — every OS process visible to `ps -ax` on
// the local host. Identity is `(host_id, pid)` formatted as `<host>:<pid>`.
//
// Hot path (always populated by the watcher tick):
//   - pid, ppid, command (basename), tty, cpuPercent, memBytes, startedAt
//
// Slow path (opt-in; the resolver pays per requested field):
//   - args:  separate `ps -wwax -o pid,args` lookup
//   - cwd:   per-pid `lsof -a -d cwd -p <pid>` (macOS) / /proc/<pid>/cwd (Linux)
//
// Watcher: poll every 3s. There is no native push for ps; we re-run
// FetchAll, diff, and emit InvalidationEvents for every key whose value
// changed (including removed keys whose process died).
//
// See ADR-011, plans/2026-05-04-orchard-implementation-guide.md, and
// .claude-context/BRIEFING.md (this workstream's spec).
package ps
