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
#    1. Upstream noise. Astro's bundler reports that it cannot guarantee the
#       semantics of the "use astro:head-inject" directive Starlight's own MDX
#       pipeline emits. Nothing in this repository produces or can suppress it.
#       There is no separate entry for "use astro:head-inject": it appears on
#       the SAME line, so it never matched, and the stale check below caught it
#       as dead the first time it ran.
#    2. Starlight asks its content collection for a `404` entry, does not find
#       one, and renders its built-in 404 page. dist/404.html is produced.
#    3. `site:` is deliberately unset until DNS is pointed, so the sitemap
#       integration has no absolute base and skips. Setting it is step 4 of
#       web/README.md's publish checklist — at which point this entry goes
#       stale and the check below fails until it is removed.
#    4. The docs are monolingual, so the optional i18n collection is empty.
known=(
  'MODULE_LEVEL_DIRECTIVE'
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

failed=0

# Which known[] entries actually matched something this run.
hits=()
for _ in "${known[@]}"; do hits+=(0); done

while IFS= read -r line; do
  [ -n "${line}" ] || continue
  matched=0
  for i in "${!known[@]}"; do
    case "${line}" in
      *"${known[$i]}"*) hits[$i]=1; matched=1; break ;;
    esac
  done
  if [ "${matched}" -eq 0 ]; then
    echo "    UNEXPECTED: ${line}" >&2
    failed=1
  fi
done <<< "${warnings}"

if [ "${failed}" -ne 0 ]; then
  echo "check-warnings: the warning(s) above are not in the known list." >&2
  echo "check-warnings: fix them, or add them to known[] in $(basename "${BASH_SOURCE[0]}") with a reason." >&2
fi

# A known[] entry that matched NOTHING is a warning that has stopped being
# emitted, and its entry is now a standing excuse for a warning nobody sees.
#
# The sitemap entry is the one that matters: it is there only because `site:`
# is unset, and the comment above promised this check would "hold whoever
# points DNS" to removing it. It would not have — the loop only ever flagged
# warnings missing from the list, never list entries missing from the
# warnings. That promise is now true rather than asserted.
for i in "${!known[@]}"; do
  if [ "${hits[$i]}" -eq 0 ]; then
    echo "    STALE: no warning matched known[] entry: ${known[$i]}" >&2
    failed=1
  fi
done

if [ "${failed}" -ne 0 ]; then
  echo "check-warnings: a STALE entry means that warning is gone — delete its" >&2
  echo "check-warnings: entry from known[] so the list says what the build says." >&2
  exit 1
fi

echo "==> $(printf '%s\n' "${warnings}" | wc -l | tr -d ' ') build warning(s), all known and accounted for"
echo "    (and every known[] entry matched — none is stale)"
