#!/usr/bin/env bash
# collapse-statement.sh — collapse a statement file into the one-line shape
# the conversation-contracts jsonl expects. Single source of truth shared by
# the SessionStart hook (which writes the sentinel) and the bats tests
# (which compute the expected value). Extracting this prevents co-drift:
# if the normalization rules ever change (e.g. add tab/CRLF handling), the
# hook and the test cannot disagree because they share this script.
#
# Usage: bash collapse-statement.sh <path-to-statement-file>
# Prints the collapsed statement to stdout. Exits 1 if the file is missing.

set -uo pipefail

file="${1:-}"
if [ -z "$file" ] || [ ! -r "$file" ]; then
  echo "collapse-statement.sh: usage: $0 <readable-file>" >&2
  exit 1
fi

# Normalize all whitespace shapes to spaces, then collapse runs and strip ends.
# Handles: CRLF (strip \r first), tabs (tr maps to space), newlines (tr maps
# to space), runs (sed collapses), leading + trailing space (sed strips).
# Without these, a statement file with Windows line endings or leading tabs
# would leak control characters into the sentinel JSON.
sed 's/\r$//' "$file" | tr '\n\t' '  ' | sed 's/  */ /g; s/^ *//; s/ *$//'
