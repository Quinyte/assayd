# SPDX-FileCopyrightText: 2026 Quinyte
# SPDX-License-Identifier: Apache-2.0
"""Refuse two claim badges that share one enclosing block.

The repository's HTML gate scopes a retraction to the ENCLOSING BLOCK, and a
block there is coarser than it looks: a `<div>` of inline `<span>` pills is one
block, and so is a table — bigger than a table cell. PR #60 measured a
correction in one pill rescuing a planted false claim in the pill beside it.

So a badge and its claim must render as one block, and two claims must never
share one. The inline badge is a `<span>`, deliberately, because it has to sit
in a sentence — which means TWO of them in one paragraph share that paragraph's
block. Nothing about the component can prevent that: a badge that forced its
own block would not be usable inline, which is the thing it exists for. It is
therefore checked here rather than designed away, and this file is the check.

Why a parser and not a regex: the question is "what is the nearest block-level
ANCESTOR", which is a tree question. A regex over the text would have to guess
at nesting, and guessing is how a gate develops a false pass.

The block set below is a deliberate under-approximation. `td`, `th` and `tr`
are NOT boundaries, because the gate this mirrors was measured treating a block
as bigger than a table cell; counting them would let two claims in one row pass
here and merge there.
"""

from __future__ import annotations

import sys
from html.parser import HTMLParser

# Elements that start a new block for this purpose. Deliberately coarse: every
# element ABSENT from this set makes a collision more likely to be reported,
# which is the safe direction for a gate.
BLOCK = {
    "address", "article", "aside", "blockquote", "body", "dd", "details",
    "dialog", "div", "dl", "dt", "fieldset", "figcaption", "figure", "footer",
    "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hgroup", "li",
    "main", "nav", "ol", "p", "pre", "section", "table", "ul",
}

# Void elements never open a scope, so they must not be pushed onto the stack.
VOID = {
    "area", "base", "br", "col", "embed", "hr", "img", "input", "link",
    "meta", "param", "source", "track", "wbr",
}


class ClaimBlocks(HTMLParser):
    """Collect every claim badge, keyed by the block instance that encloses it.

    Each opened element gets a serial number at PUSH time, so a block's identity
    is fixed when it opens. Keying on the parser's current line instead would
    give two badges in one paragraph two different keys whenever they sat on
    different source lines — a false pass, in the check whose whole job is to
    prevent one.
    """

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.stack: list[tuple[str, int]] = []
        self.serial = 0
        # (block serial, block tag) -> claims found directly inside it
        self.found: dict[tuple[int, str], list[tuple[str, str]]] = {}

    # -- element bookkeeping -------------------------------------------------

    def handle_starttag(self, tag, attrs) -> None:
        # PUSH FIRST, then record. The element carrying the claim has to be on
        # the stack when the enclosing block is resolved, because a badge that
        # is ITSELF block-level is its own block — the `header` variant renders
        # a <div>, and three of those are three blocks, not three claims
        # sharing their parent. Recording before the push attributed every
        # header badge to whatever contained it, which reported a collision
        # that is not one.
        if tag not in VOID:
            self.serial += 1
            self.stack.append((tag, self.serial))
        self._record(attrs)

    def handle_startendtag(self, tag, attrs) -> None:
        # Opens and closes at once, so it is never pushed. A self-closing
        # block-level element still gets its own block identity.
        if tag in BLOCK:
            self.serial += 1
            self.stack.append((tag, self.serial))
            self._record(attrs)
            self.stack.pop()
        else:
            self._record(attrs)

    def handle_endtag(self, tag) -> None:
        if tag in VOID:
            return
        # Tolerate unbalanced markup rather than desynchronising the stack:
        # unwind to the most recent matching open tag if there is one, and
        # ignore a stray close tag entirely.
        for i in range(len(self.stack) - 1, -1, -1):
            if self.stack[i][0] == tag:
                del self.stack[i:]
                return

    # -- the actual question -------------------------------------------------

    def _record(self, attrs) -> None:
        d = {k: (v or "") for k, v in attrs}
        if "data-claim" not in d:
            return
        evidence = d.get("data-claim-test") or d.get("data-claim-design") or "—"
        self.found.setdefault(self._enclosing_block(), []).append(
            (d["data-claim"], evidence)
        )

    def _enclosing_block(self) -> tuple[int, str]:
        for tag, serial in reversed(self.stack):
            if tag in BLOCK:
                return (serial, tag)
        return (0, "<document root>")


def check(path: str) -> list[str]:
    parser = ClaimBlocks()
    with open(path, encoding="utf-8") as fh:
        parser.feed(fh.read())
    parser.close()

    problems = []
    for (_serial, tag), claims in parser.found.items():
        if len(claims) > 1:
            listed = ", ".join(f"{c} ({e})" for c, e in claims)
            problems.append(
                f"{path}: {len(claims)} claims share one <{tag}> block: {listed}"
            )
    return problems


def main(argv: list[str]) -> int:
    problems: list[str] = []
    for path in argv[1:]:
        problems.extend(check(path))
    for p in problems:
        print(f"    FAIL {p}", file=sys.stderr)
    if problems:
        print(
            "    A retraction's rescue scope is the enclosing block, so two "
            "claims sharing one block means either can read as covered by the "
            "other's evidence. Put each claim in its own block.",
            file=sys.stderr,
        )
    return 1 if problems else 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
