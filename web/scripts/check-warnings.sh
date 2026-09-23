#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Quinyte
# SPDX-License-Identifier: Apache-2.0
#
# Fail on any build warning that is not a known one.
#
# The build emits four warnings and exits 0, and the PR that introduced it
# reported "0 errors, 0 warnings" — which was `astro check`'s number, not the
# build's. Rather than restate that more carefully, this makes the set of
# tolerated warnings an explicit list: the four below are accounted for, and a
# fifth fails CI until someone accounts for it too.
#
# Usage:  web/scripts/check-warnings.sh          # reads web/.build.log
#         web/scripts/check-warnings.sh <log>

set -euo pipefail

web_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
log="${1:-${web_dir}/.build.log}"

if [ ! -f "${log}" ]; then
  echo "check-warnings: ${log} does not exist — run 'pnpm run build' first." >&2
  exit 1
fi

# Each entry is a fixed substring, and each one is here with a reason. None is
# a wildcard: a pattern broad enough to absorb an unrelated warning would make
# this file worse than not having it.
#
#  1-2. Upstream noise. Astro's bundler reports that it cannot guarantee the
#       semantics of the "use astro:head-inject" directive Starlight's own MDX
#       pipeline emits. Nothing in this repository produces or can suppress it.
#    3. Starlight asks its content collection for a `404` entry, does not find
#       one, and renders its built-in 404 page. dist/404.html is produced.
#    4. `site:` is deliberately unset until DNS is pointed, so the sitemap
#       integration has no absolute base and skips. Setting it is step 4 of
#       web/README.md's publish checklist — at which point this entry should
#       be REMOVED, and this check will hold whoever does it to that.
#    5. The docs are monolingual, so the optional i18n collection is empty.
known=(
  'MODULE_LEVEL_DIRECTIVE'
  'use astro:head-inject'
  'Entry docs → 404 was not found'
  'The Sitemap integration requires the `site` astro.config option'
  'The collection "i18n" does not exist or is empty'
)

# Strip ANSI colour before matching; the build writes escape codes even to a
# pipe, and they sit in the middle of the messages.
warnings="$(sed 's/\x1b\[[0-9;]*m//g' "${log}" | grep -F '[WARN]' || true)"

if [ -z "${warnings}" ]; then
  echo "==> build emitted no warnings"
  exit 0
fi

unexpected=0
while IFS= read -r line; do
  [ -n "${line}" ] || continue
  matched=0
  for k in "${known[@]}"; do
    case "${line}" in
      *"${k}"*) matched=1; break ;;
    esac
  done
  if [ "${matched}" -eq 0 ]; then
    echo "    UNEXPECTED: ${line}" >&2
    unexpected=1
  fi
done <<< "${warnings}"

if [ "${unexpected}" -ne 0 ]; then
  echo "check-warnings: the warning(s) above are not in the known list." >&2
  echo "check-warnings: fix them, or add them to known[] in $(basename "${BASH_SOURCE[0]}") with a reason." >&2
  exit 1
fi

echo "==> $(printf '%s\n' "${warnings}" | wc -l | tr -d ' ') build warning(s), all known and accounted for"
