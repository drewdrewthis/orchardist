#!/usr/bin/env bash
# check-feature-parity-daemon.sh — verify every daemon scenario has a matching
# // @scenario annotation in daemon/**/*_test.go, and every annotation resolves
# to a scenario title.
#
# Exit codes:
#   0 — all scenarios bound; all annotations resolve
#   1 — mismatches found
#
# Options:
#   --json    emit a JSON report to stdout instead of human-readable text
#
# Philosophy: zero tolerance. No @unimplemented, no LEGACY_UNBOUND, no skip-list.
# See docs/testing-philosophy.md.

set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
FEATURES_DIR="$SCRIPT_DIR/../daemon/features"
TESTS_ROOT="$SCRIPT_DIR/.."
JSON_MODE=false

for arg in "$@"; do
  [[ "$arg" == "--json" ]] && JSON_MODE=true
done

# ---------------------------------------------------------------------------
# 1. Extract all scenario titles from .feature files.
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
# 2. Extract all // @scenario <title> annotations from *_test.go files.
# ---------------------------------------------------------------------------
declare -A annotation_to_file
all_annotations=()

while IFS= read -r -d '' test_file; do
  while IFS= read -r line; do
    # Only match lines where @scenario immediately follows // (with optional spaces).
    # Pattern: ^<whitespace>// <whitespace>@scenario <whitespace><title>
    if [[ "$line" =~ ^[[:space:]]*//[[:space:]]*@scenario[[:space:]]+(.+)$ ]]; then
      title="${BASH_REMATCH[1]}"
      title="${title%%[[:space:]]}"
      annotation_to_file["$title"]="$test_file"
      all_annotations+=("$title")
    fi
  done < "$test_file"
done < <(find "$TESTS_ROOT/daemon" -name "*_test.go" -print0 2>/dev/null || true)

# ---------------------------------------------------------------------------
# 3. Check: every scenario must have at least one annotation.
# ---------------------------------------------------------------------------
unbound_scenarios=()
for title in "${all_scenarios[@]}"; do
  if [[ -z "${annotation_to_file[$title]+_}" ]]; then
    unbound_scenarios+=("$title")
  fi
done

# ---------------------------------------------------------------------------
# 4. Check: every annotation must resolve to a scenario.
# ---------------------------------------------------------------------------
unknown_annotations=()
for title in "${all_annotations[@]}"; do
  if [[ -z "${scenario_to_file[$title]+_}" ]]; then
    unknown_annotations+=("$title")
  fi
done

# ---------------------------------------------------------------------------
# 5. Report
# ---------------------------------------------------------------------------
has_errors=false
[ "${#unbound_scenarios[@]}" -gt 0 ] && has_errors=true
[ "${#unknown_annotations[@]}" -gt 0 ] && has_errors=true

scenario_count="${#all_scenarios[@]}"
annotation_count="${#all_annotations[@]}"
unbound_count="${#unbound_scenarios[@]}"
unknown_count="${#unknown_annotations[@]}"

if [[ "$JSON_MODE" == "true" ]]; then
  pass="true"
  $has_errors && pass="false" || true
  echo "{\"pass\":$pass,\"scenariosTotal\":$scenario_count,\"annotationsTotal\":$annotation_count,\"unboundCount\":$unbound_count,\"unknownCount\":$unknown_count}"
else
  echo "=== daemon feature parity check ==="
  echo "  scenarios:   $scenario_count"
  echo "  annotations: $annotation_count"
  echo ""

  if [ "${#unbound_scenarios[@]}" -gt 0 ]; then
    echo "FAIL: $unbound_count scenario(s) have no matching // @scenario annotation:"
    for title in "${unbound_scenarios[@]}"; do
      echo "  - [UNBOUND] $title"
      echo "    in: ${scenario_to_file[$title]}"
    done
    echo ""
  fi

  if [ "${#unknown_annotations[@]}" -gt 0 ]; then
    echo "FAIL: $unknown_count annotation(s) do not match any scenario title:"
    for title in "${unknown_annotations[@]}"; do
      echo "  - [UNKNOWN] $title"
      echo "    in: ${annotation_to_file[$title]}"
    done
    echo ""
  fi

  if [[ "$has_errors" == "false" ]]; then
    echo "PASS: all $scenario_count daemon scenarios are bound to tests."
  fi
fi

if [[ "$has_errors" == "true" ]]; then
  exit 1
fi
