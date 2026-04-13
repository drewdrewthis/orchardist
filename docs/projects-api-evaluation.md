# ProjectV2 API Evaluation for Workflow State Tracking

Issue: #235 — evaluate whether GitHub Projects API (ProjectV2) provides workflow state data that labels don't.

## Fields Exposed

ProjectV2 provides **rich typed fields** beyond labels: Status (single-select), Priority (single-select), Iteration (date-bounded sprints), custom Text/Number/Date fields, and Assignees. The key differentiator is **Status** — a single-select field with ordered options (e.g., "Backlog", "In Progress", "In Review", "Done") that maps directly to kanban columns.

## Comparison to Labels

Labels are untyped strings with no ordering, mutual exclusion, or transitions. ProjectV2 Status is a **single-select enum** — an issue can only be in one status at a time, and the options are explicitly ordered. This is precisely what workflow phase tracking needs: mutually exclusive states with defined transitions. Labels require convention enforcement (remove old label when adding new one); Status enforces it structurally.

## API Complexity

Issue/PR nodes expose a `projectItems` connection directly, queryable inline with existing GraphQL:

```graphql
projectItems(first: 5) {
  nodes {
    fieldValueByName(name: "Status") {
      ... on ProjectV2ItemFieldSingleSelectValue { name }
    }
  }
}
```

**Authentication**: Requires `project` scope on the PAT (or `read:project` for read-only). The existing `gh` CLI token may not have this — users would need to re-auth. Rate limits are standard GraphQL (5,000 points/hour), and adding `projectItems` costs minimal additional points.

## Requires a Project Board?

**Yes.** The repo/org must have a ProjectV2 board, and issues must be added to it. If no project exists, `projectItems` returns empty. This is a hard prerequisite that labels don't have — labels work out of the box.

## Blast Radius for Orchard

- **Query changes**: Add `projectItems` fragment to existing GraphQL queries in `cache_sources.rs` — additive.
- **Cache types**: Add optional `project_status: Option<String>` to `CachedIssue`/`CachedPr`. Backward-compatible with `#[serde(default)]`.
- **Config**: New optional field in `.orchard.json` for project board ID and field name mapping. Without config, feature is inert.
- **New dependencies**: None. Uses existing `gh api graphql` path.
- **New cache files**: None — data attaches to existing issue/PR caches.
- **Auth**: May require PAT scope upgrade (`project` or `read:project`) — biggest operational friction.

## Recommendation

**Not yet.** Labels are sufficient for Orchard's current needs. ProjectV2 is the right tool if/when enforced workflow states are needed, but it adds a hard dependency on project board setup and PAT scope. Expose raw labels first (issue #235); revisit ProjectV2 when the label-based approach hits its limits (stale dual-labels, missing transitions).

## Future Design (if adopted)

If ProjectV2 is integrated later, the proposed shape:

1. Optional config in `.orchard.json`: `{ "project": { "id": "PVT_...", "statusField": "Status" } }`
2. Add `project_status: Option<String>` to `CachedIssue`/`CachedPr` with `#[serde(default)]`
3. Thread through to `IssueInfo`/`PrState` → `JsonIssue`/`JsonPr`
4. Coexist with labels: `phase` stays label-derived, new `projectStatus` field is ProjectV2-derived
5. Consumers choose which to trust based on their setup
