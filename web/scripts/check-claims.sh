#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Quinyte
# SPDX-License-Identifier: Apache-2.0
#
# Every claim badge in the built output must name evidence that exists.
#
# claim-class.ts refuses a `measured` badge that names NO test. That is the
# weaker half of the problem: the live failure is the rename or the delete, not
# the omission. A badge reading "Proven by the test TestThatWasRenamedLastMonth"
# builds, ships, and is read aloud to a screen reader as proof — on a site whose
# whole argument is that a claim must carry checkable evidence. This is the gate
# for that.
#
# WHAT IT CHECKS, exactly:
#   measured  -> a top-level `func <name>(` exists under test/, internal/ or api/
#   designed  -> docs/designs/<NN>-*.md exists for a "design <NN>" reference
#
# WHAT IT DOES NOT CHECK, and nobody should read it as checking:
#   - that the named test PASSES. `make test` is what says that.
#   - that the named test actually proves the sentence the badge sits beside.
#     That is a human judgement and no script can make it.
#   - `not-built`, which asserts the absence of code and so has nothing to
#     resolve.
#
# Usage:  web/scripts/check-claims.sh

set -euo pipefail

web_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(cd "${web_dir}/.." && pwd)"

status=0
total_claims=0

for target in site docs; do
  dist="${web_dir}/${target}/dist"

  # Same guard as check-links.sh, for the same reason: a gate that examined
  # nothing must not report success.
  if [ ! -d "${dist}" ]; then
    echo "check-claims: ${target}: ${dist} does not exist — run 'pnpm run build' first." >&2
    exit 1
  fi
  if [ "$(find "${dist}" -name '*.html' -type f | wc -l | tr -d ' ')" -eq 0 ]; then
    echo "check-claims: ${target}: no .html files under ${dist} — refusing to report success." >&2
    exit 1
  fi

  # -o prints one match per line, so a page carrying several badges yields
  # several rows rather than only its first.
  measured="$(
    grep -rhoE 'data-claim-test="[^"]+"' "${dist}" --include='*.html' \
      | sed 's/^data-claim-test="//; s/"$//' | sort -u || true
  )"
  designed="$(
    grep -rhoE 'data-claim-design="[^"]+"' "${dist}" --include='*.html' \
      | sed 's/^data-claim-design="//; s/"$//' | sort -u || true
  )"

  while IFS= read -r test_name; do
    [ -n "${test_name}" ] || continue
    total_claims=$((total_claims + 1))
    # Anchored at the start of a line, so a mention in a comment, a string or a
    # call site cannot satisfy it — only a top-level declaration can. Go test
    # functions are always top-level, so this is exactly the right anchor.
    if grep -rqE "^func ${test_name}\(" \
        --include='*.go' "${repo_root}/test" "${repo_root}/internal" "${repo_root}/api"; then
      echo "    ok   measured: ${test_name}"
    else
      echo "    FAIL measured: ${test_name} — no 'func ${test_name}(' under test/, internal/ or api/" >&2
      status=1
    fi
  done <<< "${measured}"

  while IFS= read -r design_ref; do
    [ -n "${design_ref}" ] || continue
    total_claims=$((total_claims + 1))
    # "design 03" -> docs/designs/03-*.md
    num="$(printf '%s' "${design_ref}" | grep -oE '[0-9]+' | head -1 || true)"
    if [ -n "${num}" ] && compgen -G "${repo_root}/docs/designs/${num}-*.md" >/dev/null; then
      echo "    ok   designed: ${design_ref}"
    else
      echo "    FAIL designed: ${design_ref} — no docs/designs/${num:-??}-*.md" >&2
      status=1
    fi
  done <<< "${designed}"

  echo "==> ${target}: claims resolved above"
done

# A build that rendered no badges at all would otherwise pass this silently,
# and the badge is the one component the whole site depends on.
if [ "${total_claims}" -eq 0 ]; then
  echo "check-claims: no claim badges found in either target — refusing to report success." >&2
  exit 1
fi

if [ "${status}" -eq 0 ]; then
  echo "==> ${total_claims} claim(s) resolve to evidence that exists"
  echo "    (existence only — 'make test' is what says a test passes)"
fi
exit "${status}"
