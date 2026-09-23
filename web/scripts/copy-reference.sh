#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Quinyte
# SPDX-License-Identifier: Apache-2.0
#
# Copy the generated reference into the docs target's content tree.
#
# docs/reference/*.md is written by cmd/refgen and held to the code by
# `make verify`; it is the one committed copy. This script copies it into
# web/docs/src/content/docs/reference/ (gitignored) BEFORE the Astro build, so
# that check-links.sh, which reads the built dist/, checks every link in the
# copied pages. A copy made after the build, or by hand into the content tree,
# would publish those pages' anchors unchecked.
#
# @assayd/docs runs this as the first step of its `build` and `dev` scripts,
# not only from scripts/build.sh: an earlier version ran it from build.sh alone,
# and `pnpm run build:docs` then produced a docs site with no reference and
# three dead sidebar links while exiting 0 (independent review of PR #67).
#
# The destination is emptied first, so a page deleted from docs/reference/
# drops out of the site rather than lingering from an earlier copy.

set -euo pipefail

web_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(cd "${web_dir}/.." && pwd)"
src="${repo_root}/docs/reference"
dst="${web_dir}/docs/src/content/docs/reference"

shopt -s nullglob
pages=("${src}"/*.md)
shopt -u nullglob
if [ "${#pages[@]}" -eq 0 ]; then
  # With no pages the build would still pass every gate: the link check would
  # simply see fewer of them. Refuse instead.
  echo "copy-reference: no generated reference under ${src} — run 'make reference'." >&2
  exit 1
fi
rm -rf "${dst}"
mkdir -p "${dst}"
cp "${pages[@]}" "${dst}/"
echo "copy-reference: copied ${#pages[@]} page(s) from docs/reference/"
