#!/usr/bin/env bash
set -euo pipefail

# Verify every suite tarball's binaries were built from ONE clean commit.
#
# The Go stampable set and the --revision-answering set are read from
# internal/release via `go run ./internal/release/cmd/suite-bins` (orchardist
# #820) rather than hand-mirrored here, so this list cannot drift from
# SuiteBinaries / RevisionBinaries. Because of that read, this script now
# requires the MODULE SOURCE checked out and a Go toolchain -- not just a
# downloaded tarball. In CI it always runs from the repo checkout (the
# checksums job checks out the repo and installs Go), so this holds; a local
# or offline run must be inside the repo with `go` on PATH.
#
# Revision provenance is checked STATICALLY for every tarball, host or foreign:
#   - Go binaries: `go version -m` reads the -X ...release.revision ldflag.
#   - orchard-tui (Rust, stamped): a greppable `ORCHARD_REVISION=<sha>` marker
#     embedded by build.rs + main.rs, extracted without executing the binary,
#     so a foreign-arch orchard-tui is verified too (was SKIPped before, #820).
# On the host triple only, orchard-tui is additionally executed and its
# --revision output asserted equal to the embedded marker, so one arch
# cross-validates that the static marker faithfully mirrors runtime.
# Guards #817: untracked build outputs made buildvcs stamp binaries "+dirty".

[ "$#" -ge 1 ] || { echo "usage: $0 <suite-tarball>..." >&2; exit 2; }

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_bins_raw="$(cd "$ROOT" && go run ./internal/release/cmd/suite-bins go)"
rev_bins_raw="$(cd "$ROOT" && go run ./internal/release/cmd/suite-bins revision)"
# shellcheck disable=SC2206  # one binary name per line, no spaces -- word-split is intended
GO_BINS=($go_bins_raw)
# shellcheck disable=SC2206
REV_BINS=($rev_bins_raw)

# Non-Go revision binaries: the stamped set minus the Go set. Currently just
# orchard-tui (Rust). Verified via the static ORCHARD_REVISION marker so no
# foreign-arch binary goes unchecked.
NONGO_REV_BINS=()
for bin in "${REV_BINS[@]}"; do
  skip=0
  for g in "${GO_BINS[@]}"; do [ "$bin" = "$g" ] && skip=1 && break; done
  [ "$skip" -eq 0 ] && NONGO_REV_BINS+=("$bin")
done

# Host triple (mirrors internal/release triples map): the one triple whose
# binaries can be executed here, used only for the orchard-tui static==exec
# cross-check below.
case "$(go env GOOS)/$(go env GOARCH)" in
  darwin/amd64) HOST_TRIPLE=x86_64-apple-darwin ;;
  darwin/arm64) HOST_TRIPLE=aarch64-apple-darwin ;;
  linux/amd64)  HOST_TRIPLE=x86_64-unknown-linux-gnu ;;
  linux/arm64)  HOST_TRIPLE=aarch64-unknown-linux-gnu ;;
  *)            HOST_TRIPLE="" ;;
esac

# record_rev BIN REV COL2 EMPTY_MSG [EXTRA_MSG] -- the per-binary bookkeeping
# shared by the Go and non-Go loops below: print the table row, fail on a
# missing, +dirty, caller-supplied-extra, or cross-binary-mismatched revision,
# and update seen_rev. EXTRA_MSG (only the Go loop's vcs.modified=true check
# uses it) is slotted between the dirty and mismatch checks to keep FAIL-line
# order identical to the pre-refactor loop. One implementation so the two
# loops' checks cannot drift apart.
record_rev() {
  local bin="$1" rev="$2" col2="$3" empty_msg="$4" extra_msg="${5:-}"
  printf '  %-18s %-14s %s\n' "$bin" "$col2" "${rev:-<none>}"
  if [ -z "$rev" ]; then echo "  FAIL $bin: $empty_msg" >&2; fail=1; fi
  case "$rev" in *+dirty*) echo "  FAIL $bin: revision is +dirty" >&2; fail=1 ;; esac
  if [ -n "$extra_msg" ]; then echo "  FAIL $bin: $extra_msg" >&2; fail=1; fi
  if [ -n "$rev" ] && [ -n "$seen_rev" ] && [ "$rev" != "$seen_rev" ]; then
    echo "  FAIL $bin: revision $rev differs from $seen_rev" >&2; fail=1
  fi
  [ -n "$rev" ] && seen_rev="$rev"
}

fail=0
expected_rev=""
for tarball in "$@"; do
  echo "== $tarball =="
  work="$(mktemp -d)"
  trap 'rm -rf "$work"' EXIT
  tar xzf "$tarball" -C "$work"
  printf '  %-18s %-14s %s\n' BINARY VCS.MODIFIED REVISION

  is_host=0
  if [ -n "$HOST_TRIPLE" ] && printf '%s' "$tarball" | grep -q "$HOST_TRIPLE"; then
    is_host=1
  fi

  # Seed with the revision established by prior tarballs so a mismatch
  # ACROSS tarballs fails too, not just within one (#817 skew case).
  seen_rev="$expected_rev"

  # --- Go binaries: static via `go version -m` (works cross-arch). ---
  for bin in "${GO_BINS[@]}"; do
    path="$work/$bin"
    [ -f "$path" ] || continue
    meta="$(go version -m "$path")"
    rev="$(printf '%s' "$meta" | grep -oE 'internal/release\.revision=[^ "]+' | head -1 | cut -d= -f2- || true)"
    modified="$(printf '%s' "$meta" | grep -oE 'vcs\.modified=(true|false)' | head -1 | cut -d= -f2- || true)"
    extra=""
    [ "$modified" = "true" ] && extra="vcs.modified=true"
    record_rev "$bin" "$rev" "${modified:-<none>}" "no release.revision ldflag" "$extra"
  done

  # --- Non-Go revision binaries (Rust, stamped): static ORCHARD_REVISION
  # marker, extracted for EVERY tarball without executing the binary. ---
  # ${NONGO_REV_BINS[@]+"${NONGO_REV_BINS[@]}"} guards the empty-array case
  # under `set -u` on bash 3.2 (macOS's shipped bash), where `arr[@]` on an
  # empty array is an unset-variable error rather than zero words.
  for bin in "${NONGO_REV_BINS[@]+"${NONGO_REV_BINS[@]}"}"; do
    path="$work/$bin"
    [ -f "$path" ] || continue
    rev="$(grep -a -oE 'ORCHARD_REVISION=[0-9a-f]{7,40}(\+dirty)?' "$path" | head -1 | cut -d= -f2- || true)"
    record_rev "$bin" "$rev" "<static>" "no ORCHARD_REVISION marker"

    # Host triple: cross-validate that the static marker equals runtime.
    if [ "$is_host" -eq 1 ]; then
      out="$("$path" --revision 2>/dev/null || true)"
      echo "  --revision $bin: ${out:-<none>}"
      if [ "$out" != "$rev" ]; then
        echo "  FAIL $bin: --revision $out differs from embedded marker $rev" >&2; fail=1
      else
        echo "  OK $bin: static marker == --revision"
      fi
    fi
  done

  [ -z "$expected_rev" ] && expected_rev="$seen_rev"

  rm -rf "$work"; trap - EXIT
done

if [ "$fail" -ne 0 ]; then
  echo "revision check FAILED" >&2
  exit 1
fi
echo "revision check PASSED"
