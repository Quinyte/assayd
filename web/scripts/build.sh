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

# The generated reference is copied into the docs target by
# scripts/copy-reference.sh, which @assayd/docs runs as the first half of its
# own `build` and `dev` scripts, so it happens before every Astro build of the
# docs whichever command starts it, and check-links.sh then reads the copied
# pages in dist/.

# tee returns its own status, so without PIPESTATUS a failed build would be
# reported as a success by the pipeline.
set +e
pnpm -r --workspace-concurrency=1 run build 2>&1 | tee .build.log
rc="${PIPESTATUS[0]}"
set -e

exit "${rc}"
