#!/usr/bin/env bash
# Stop-bleed guard for orchard-gui scoped styles.
#
# Fails when a PR adds a `<style>` block to any .svelte file under
# src/lib/components/ that is NOT on the allowlist. Operates on a diff
# against a base ref so existing <style> blocks in the migration window
# don't break the build — only new additions do.
#
# Per ADR-020, new GUI work must use Tailwind utilities, not scoped CSS.
# The allowlist (.style-allowlist.txt) is for the handful of components
# that genuinely cannot — typically those wrapping third-party DOM.
#
# Usage:
#   scripts/stop-bleed-check.sh                # diff against origin/main
#   scripts/stop-bleed-check.sh <base-ref>     # diff against custom base
#
# In CI: invoke with the PR base ref (e.g. origin/main).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUI_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$GUI_ROOT/../.." && pwd)"
ALLOWLIST_FILE="$GUI_ROOT/.style-allowlist.txt"

BASE_REF="${1:-origin/main}"

# Build the allowlist set: basenames, comments stripped, blanks dropped.
declare -a allowlist=()
if [[ -f "$ALLOWLIST_FILE" ]]; then
  while IFS= read -r line; do
    name="${line%%#*}"
    name="${name#"${name%%[![:space:]]*}"}"
    name="${name%"${name##*[![:space:]]}"}"
    [[ -z "$name" ]] && continue
    allowlist+=("$name")
  done < "$ALLOWLIST_FILE"
fi

is_allowlisted() {
  local file="$1"
  local entry
  for entry in "${allowlist[@]}"; do
    if [[ "$file" == "$entry" ]]; then
      return 0
    fi
  done
  return 1
}

cd "$REPO_ROOT"

# Files in the diff under crates/orchard-gui/src/lib/components/ that are
# Added or Modified.
mapfile -t changed_files < <(
  git diff --diff-filter=AM --name-only "$BASE_REF" -- \
    'crates/orchard-gui/src/lib/components/*.svelte' 2>/dev/null || true
)

if [[ ${#changed_files[@]} -eq 0 ]]; then
  echo "stop-bleed: no relevant .svelte changes vs $BASE_REF — ok."
  exit 0
fi

violations=0
for file in "${changed_files[@]}"; do
  base="$(basename "$file")"
  if is_allowlisted "$base"; then
    continue
  fi
  # Lines added in this diff that begin a <style> block.
  added_style=$(git diff "$BASE_REF" -- "$file" \
    | grep -E '^\+[[:space:]]*<style' || true)
  if [[ -n "$added_style" ]]; then
    echo "stop-bleed: $file adds a <style> block."
    echo "  Per ADR-020, new components must style via Tailwind utilities."
    echo "  If this is a legitimate exception (third-party DOM, etc.),"
    echo "  add '$base' to crates/orchard-gui/.style-allowlist.txt with a"
    echo "  trailing-comment reason."
    violations=$((violations + 1))
  fi
done

if (( violations > 0 )); then
  echo
  echo "stop-bleed: $violations violation(s). See docs/adr/020-tailwind-css-adoption.md."
  exit 1
fi

echo "stop-bleed: ok (no new disallowed <style> blocks vs $BASE_REF)."
