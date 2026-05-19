#!/usr/bin/env bash
# check-feature-parity-tui.sh — verify every TUI scenario has a matching
# // @scenario annotation in crates/orchard/**/*.rs test files.
#
# Exit codes:
#   0 — no stale annotations; gaps are informational
#   1 — stale/unknown annotations found
#
# Options:
#   --json    emit a JSON report to stdout instead of human-readable text
#
# Note: TUI tests are follow-up work. Unbound scenarios are surfaced as gaps
# to drive implementation, not treated as failures. Unknown annotations (stale
# bindings) are failures — they silently rot.

set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
FEATURES_DIR="$SCRIPT_DIR/../crates/orchard/features"
TESTS_DIR="$SCRIPT_DIR/../crates/orchard"
JSON_MODE=false

for arg in "$@"; do
  [[ "$arg" == "--json" ]] && JSON_MODE=true
done

# ---------------------------------------------------------------------------
# 1. Extract scenario titles from .feature files.
# ---------------------------------------------------------------------------
declare -A scenario_to_file
all_scenarios=()

while IFS= read -r -d '' feature_file; do
  while IFS= read -r line; do
    if [[ "$line" =~ ^[[:space:]]+"Scenario:"[[:space:]]*(.+)$ ]]; then
      title="${BASH_REMATCH[1]}"
      title="${title%%[[:space:]]}"
      scenario_to_file["$title"]="$feature_file"
      all_scenarios+=("$title")
    fi
  done < "$feature_file"
done < <(find "$FEATURES_DIR" -name "*.feature" -print0 2>/dev/null || true)

# ---------------------------------------------------------------------------
# 2. Extract // @scenario annotations from Rust test files.
# ---------------------------------------------------------------------------
declare -A annotation_to_file
all_annotations=()

while IFS= read -r -d '' test_file; do
  while IFS= read -r line; do
    if [[ "$line" =~ ^[[:space:]]*//[[:space:]]*@scenario[[:space:]]+(.+)$ ]]; then
      title="${BASH_REMATCH[1]}"
      title="${title%%[[:space:]]}"
      annotation_to_file["$title"]="$test_file"
      all_annotations+=("$title")
    fi
  done < "$test_file"
done < <(find "$TESTS_DIR" -name "*.rs" -print0 2>/dev/null || true)

# ---------------------------------------------------------------------------
# 3. Gap analysis
# ---------------------------------------------------------------------------
unbound_scenarios=()
for title in "${all_scenarios[@]}"; do
  if [[ -z "${annotation_to_file[$title]+_}" ]]; then
    unbound_scenarios+=("$title")
  fi
done

unknown_annotations=()
for title in "${all_annotations[@]}"; do
  if [[ -z "${scenario_to_file[$title]+_}" ]]; then
    unknown_annotations+=("$title")
  fi
done

scenario_count="${#all_scenarios[@]}"
annotation_count="${#all_annotations[@]}"
unbound_count="${#unbound_scenarios[@]}"
unknown_count="${#unknown_annotations[@]}"

has_stale=false
[ "$unknown_count" -gt 0 ] && has_stale=true

# ---------------------------------------------------------------------------
# 4. Report
# ---------------------------------------------------------------------------
if [[ "$JSON_MODE" == "true" ]]; then
  pass="true"
  $has_stale && pass="false" || true
  echo "{\"pass\":$pass,\"scenariosTotal\":$scenario_count,\"annotationsTotal\":$annotation_count,\"unboundCount\":$unbound_count,\"unknownCount\":$unknown_count}"
else
  echo "=== TUI (crates/orchard) feature parity check ==="
  echo "  scenarios:   $scenario_count"
  echo "  annotations: $annotation_count"
  echo ""

  if [ "$unbound_count" -gt 0 ]; then
    echo "GAPS (follow-up work): $unbound_count TUI scenario(s) need Rust test implementations:"
    for title in "${unbound_scenarios[@]}"; do
      echo "  - $title"
    done
    echo ""
    echo "  Track with: gh issue create --title 'feat(tui): implement @scenario tests for TUI feature parity'"
    echo ""
  fi

  if [ "$unknown_count" -gt 0 ]; then
    echo "FAIL: $unknown_count annotation(s) do not match any TUI scenario:"
    for title in "${unknown_annotations[@]}"; do
      echo "  - [STALE] $title"
      echo "    in: ${annotation_to_file[$title]}"
    done
  fi

  if [[ "$has_stale" == "false" && "$unbound_count" -eq 0 ]]; then
    echo "PASS: all $scenario_count TUI scenarios are bound."
  elif [[ "$has_stale" == "false" ]]; then
    echo "STATUS: $unbound_count TUI scenarios need implementation (see above)."
  fi
fi

if [[ "$has_stale" == "true" ]]; then
  exit 1
fi
