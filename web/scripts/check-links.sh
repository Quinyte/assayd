#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Quinyte
# SPDX-License-Identifier: Apache-2.0
#
# Check the internal links and anchors of BOTH built targets.
#
# This runs against dist/, never against the Markdown or MDX source, and that
# is the entire point. Under `build.format: 'file'` Starlight rewrites its own
# navigation and does NOT rewrite links written in page prose, so a
# cross-reference spelled `/guides/example` emits verbatim and 404s on a host
# with no rewrite rules. Only a check over the built HTML sees that.
#
# It is also why `starlight-links-validator` is not used here despite being the
# obvious choice: it validates source against Astro's route table, where
# `/guides/example` and `/guides/example.html` both resolve. It passes both
# spellings and cannot tell them apart, so it cannot catch this bug at all.
#
# Usage:  web/scripts/check-links.sh          # both targets
#         web/scripts/check-links.sh docs     # one target
#
# Requires lychee on PATH (see LYCHEE_VERSION in .github/workflows/web.yml for
# the version CI pins; `brew install lychee` locally).

set -euo pipefail

web_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
targets=("${@:-}")
if [ -z "${targets[0]}" ]; then
  targets=(site docs)
fi

if ! command -v lychee >/dev/null 2>&1; then
  echo "check-links: lychee is not installed." >&2
  echo "  macOS:  brew install lychee" >&2
  echo "  other:  https://github.com/lycheeverse/lychee/releases" >&2
  exit 127
fi

status=0

for target in "${targets[@]}"; do
  dist="${web_dir}/${target}/dist"

  # GUARD 1: a missing or empty dist must not pass.
  #
  # lychee over an existing-but-empty directory reports "0 Total" and exits 0.
  # If the build were skipped, reordered, or made to write somewhere else, the
  # gate would go green having checked nothing — which is the worst outcome
  # available to a check like this. Assert there is something to check before
  # believing the result.
  if [ ! -d "${dist}" ]; then
    echo "check-links: ${target}: ${dist} does not exist — run 'pnpm run build' first." >&2
    exit 1
  fi
  html_count="$(find "${dist}" -name '*.html' -type f | wc -l | tr -d ' ')"
  if [ "${html_count}" -eq 0 ]; then
    echo "check-links: ${target}: no .html files under ${dist} — refusing to report success." >&2
    exit 1
  fi

  echo "==> ${target}: ${html_count} html file(s) under ${target}/dist"

  # Every flag below is load-bearing, and each one's default is a false pass:
  #
  #   --offline                    Only file:// is resolved; http(s) links are
  #                                reported EXCLUDED and can never fail this
  #                                gate. A flaky third-party host must not be
  #                                able to break this repository's CI.
  #   --include-fragments=...      Checks `#anchor` against id/name in the
  #                                target document. DEFAULT IS none: without
  #                                this, a dead anchor passes silently.
  #   --index-files index.html     Models the host exactly: a directory link
  #                                resolves only if that directory really has
  #                                an index.html. So `/` passes (dist/index.html
  #                                exists) and `/guides/` fails, because under
  #                                build.format 'file' dist/guides/ holds
  #                                figures.html and no index. lychee's DEFAULT
  #                                accepts any directory that exists on disk,
  #                                which passes `/guides/` — a false pass.
  #
  #                                `--index-files ''` rejects EVERY directory
  #                                link, which was measured to fail the four
  #                                `href="/"` links in the site masthead. That
  #                                models a host with no directory index at
  #                                all, and directory indexing is a different
  #                                server feature from URL rewriting: the host
  #                                serves index.html for a directory and does
  #                                not rewrite /guides/example to
  #                                guides/example.html. The stricter flag was a
  #                                false FAILURE, so it is not used.
  #   --root-dir <absolute>        Resolves root-relative `/guides/x.html`
  #                                against the dist root rather than the
  #                                filesystem root. Must be absolute.
  #
  # NEVER add --fallback-extensions html. It makes lychee resolve
  # `/guides/example` to guides/example.html and report OK — the precise bug
  # this check exists to catch. For the same reason, do not check links by
  # serving dist/ through a dev server with clean-URL rewrites: the check must
  # reproduce the rewrite-less host, not a friendlier one.
  set +e
  output="$(
    lychee \
      --offline \
      --include-fragments=anchor-only \
      --index-files index.html \
      --root-dir "${dist}" \
      --no-progress \
      "${dist}" 2>&1
  )"
  rc=$?
  set -e

  echo "${output}"

  # GUARD 2: belt and braces for the same failure as GUARD 1, one layer lower.
  # If lychee itself reports having examined nothing, that is not a pass.
  if echo "${output}" | grep -qE '🔍 0 Total|^0 Total'; then
    echo "check-links: ${target}: lychee examined 0 links — refusing to report success." >&2
    exit 1
  fi

  if [ "${rc}" -ne 0 ]; then
    echo "check-links: ${target}: broken internal link(s) above (lychee exit ${rc})." >&2
    echo "check-links: ${target}: cross-references need an explicit .html — see web/README.md." >&2
    status=1
  fi
done

if [ "${status}" -eq 0 ]; then
  echo "==> internal links and anchors OK in all checked targets"
fi
exit "${status}"
