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

    # The name is interpolated into a REGEX below, so it must first be shown to
    # be a plain identifier. Without this, `test=".*"` matched the first
    # function in the tree and certified itself — a claim with no evidence
    # behind it, produced by the gate that exists to prevent exactly that, and
    # read aloud to a screen reader as "Proven by the test .*".
    #
    # Nobody types `Test.*` by accident, so this is not about an attacker; it
    # is about the check being able to state what it verified.
    if ! printf '%s' "${test_name}" | grep -qE '^[A-Za-z0-9_]+$'; then
      echo "    FAIL measured: ${test_name} — not a Go identifier." >&2
      echo "         A test name must match ^[A-Za-z0-9_]+$; a pattern cannot be evidence." >&2
      status=1
      continue
    fi

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

  # A badge and its claim must render as one block, and two claims must never
  # share one — because a retraction's rescue scope is the enclosing block, so
  # two claims in one block means either can read as covered by the other's
  # evidence. The inline badge is a <span> so it can sit in a sentence, which
  # means two in one paragraph DO share a block; nothing about the component
  # can prevent that without making it unusable inline. Hence a check.
  if ! find "${dist}" -name '*.html' -type f -print0 \
      | xargs -0 python3 "${web_dir}/scripts/block_scope.py"; then
    status=1
  fi

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
