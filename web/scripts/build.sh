#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Quinyte
# SPDX-License-Identifier: Apache-2.0
#
# Build both targets, keeping the output so that check-warnings.sh can judge it.
#
# The log exists because "the build succeeded" and "the build said nothing new"
# are different claims, and only the first one is an exit code.

set -euo pipefail

web_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${web_dir}"

# The generated reference (docs/reference/*.md, written by cmd/refgen and held
# to the code by `make verify`) is copied into the docs target HERE, before the
# Astro build, and nowhere else — so that check-links.sh, which reads the built
# dist/, checks every link in the copied pages. A copy made after the build, or
# by hand into the content tree, would publish those pages' anchors unchecked.
#
# The destination is emptied first, so a page deleted from docs/reference/
# drops out of the site rather than lingering from an earlier build, and it is
# gitignored: docs/reference/ is the one committed copy.
repo_root="$(cd "${web_dir}/.." && pwd)"
ref_src="${repo_root}/docs/reference"
ref_dst="${web_dir}/docs/src/content/docs/reference"
shopt -s nullglob
ref_pages=("${ref_src}"/*.md)
shopt -u nullglob
if [ "${#ref_pages[@]}" -eq 0 ]; then
  # A build with no reference would pass every gate: the link check would
  # simply see fewer pages. Refuse rather than ship the site without it.
  echo "build: no generated reference under ${ref_src} — run 'make reference'." >&2
  exit 1
fi
rm -rf "${ref_dst}"
mkdir -p "${ref_dst}"
cp "${ref_pages[@]}" "${ref_dst}/"
echo "build: copied ${#ref_pages[@]} reference page(s) from docs/reference/"

# tee returns its own status, so without PIPESTATUS a failed build would be
# reported as a success by the pipeline.
set +e
pnpm -r --workspace-concurrency=1 run build 2>&1 | tee .build.log
rc="${PIPESTATUS[0]}"
set -e

exit "${rc}"
