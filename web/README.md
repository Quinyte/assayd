# web — the assayd.io and assayd.dev scaffold

**This is a scaffold. It builds, and it carries no content.** Two placeholder
pages per target exist to prove the layout; the information architecture, the
landing narrative, the generated reference and the diagram set all belong to
other work. Writing page content here would collide with it.

| Package | Deploy target | What it is |
|---|---|---|
| `site` | `assayd.io` | The site — landing, concepts, roadmap, releases. Astro, static. |
| `docs` | `assayd.dev` | The docs — guides, generated reference, status, archive. Astro + Starlight. |
| `shared` | neither | Design tokens and the components both targets use. Not published. |

## Why it is generated and not hand-maintained

`docs/architecture.md` and `docs/architecture.html` are two hand-maintained
copies of the same claims with no generator between them, and they have already
drifted. `shared` exists so that a token or a component cannot be changed for
one target and missed by the other: both consume it over the pnpm workspace
protocol, so there is one copy and the build fails if it is broken.

## Commands

```sh
cd web
pnpm install --frozen-lockfile
pnpm run build          # both targets -> site/dist and docs/dist
pnpm run check          # typecheck all three packages
pnpm run check:warnings # every build warning is a known one
pnpm run check:links    # internal links + anchors, over the BUILT output
pnpm run check:claims   # every claim badge names evidence that exists
pnpm run dev:site       # local preview of assayd.io
pnpm run dev:docs       # local preview of assayd.dev
```

The three `check:*` gates read what the build produced, so run `build` first.
`check:links` needs `lychee` on PATH (`brew install lychee`; CI pins the
version and its sha256 in `.github/workflows/web.yml`).

**`pnpm run check` reports "0 errors, 0 warnings" — that is `astro check`'s
number, not the build's.** The build itself emits warnings and exits 0.
`scripts/check-warnings.sh` holds four known KINDS of warning, each a fixed
substring with its reason, and fails CI on any warning matching none of them,
and on any entry nothing matched. It does not fix the COUNT: one entry, the
MDX `MODULE_LEVEL_DIRECTIVE` notice, is emitted once per MDX page, so the
number of warning lines grows with the pages (six at the time of writing) and
a new MDX page passes without an edit here. One entry is the sitemap skipping
for want of `site:`, so whoever points DNS has to come back and remove it.

`pnpm run build` also copies the generated reference (`docs/reference/*.md`)
into the docs target first; `scripts/copy-reference.sh` does it, as the first
step of `@assayd/docs`'s own `build` and `dev`, and refuses when there is
nothing to copy. The copy is gitignored: edit the generator, never the copy.

Node and pnpm are pinned exactly — `.nvmrc` and `package.json#packageManager` —
and `.npmrc` sets `engine-strict`, so an install on a different Node is refused
rather than silently producing a different `dist/`.

## The constraint everything follows from

The host is a **plain static host**: built files are pushed, and at serve time
there is no Node runtime, no SSR, no edge function and no rewrite rule. Hence:

- `output: "static"` on both targets, stated rather than left to the default.
- `build.format: "file"`, so pages are `about.html` and not `about/index.html`.
  On a host with no directory-index configuration, folder-index output makes
  every URL depend on server behaviour this project does not control.
- No Mermaid and no diagram-as-code integration. Every diagram is a generated
  `.webp` plate placed with `Figure`.

### Authoring hazard: `.html` in prose links

Measured on Astro 7.3.3 + Starlight 0.42.2: under `build.format: "file"`,
Starlight rewrites **its own** navigation and canonical links, and does **not**
rewrite links written in page prose. Of five link spellings tried in a page
body, all five emitted verbatim — only `/guides/example.html` resolved.

**Write every cross-reference with an explicit `.html`.** This IS enforced:
`pnpm run check:links` runs in CI over the built output and fails on it.

`starlight-links-validator` — the obvious choice — **cannot catch this bug**.
It validates Markdown/MDX source against Astro's route table, where
`/guides/example` and `/guides/example.html` both resolve; it passes both
spellings and cannot tell them apart. The check has to read the built HTML.

The flags in `scripts/check-links.sh` are each load-bearing, and each default
is a false pass. Measured, with a deliberately bad link in place:

| Mutation | Result |
|---|---|
| add `--fallback-extensions html` | **0 errors** — the missing `.html` passes silently |
| drop `--include-fragments` | **0 errors** — a dead `#anchor` passes silently |
| drop `--root-dir` | 16 errors — every root-relative link breaks (false failure) |

`--index-files ''` rejects every bare-directory link, so no URL on either
target depends on the server's directory-index behaviour — the same thing the
Astro config gives as its reason for `build.format: 'file'`. The site masthead
linked `href="/"` and had to be changed to `/index.html` to satisfy it; that
was the masthead contradicting the config, not the flag being wrong.

## Components

### `ClaimClass`

Three states and deliberately no fourth. A claim is proven by a named test, or
written in a design and enforced by nothing, or not built.

```astro
<ClaimClass claim="measured" test="TestAnAgentAnswersThroughTheGateway" />
<ClaimClass claim="designed" design="design 03" section="§3.3.2" />
<ClaimClass claim="not-built" note="design 10 does not exist yet" />
<ClaimClass variant="header" claim="measured" test="TestTheGatewayIsTheOnlyWayIn" />
```

`measured` without a test, or `designed` without a design and section, **fails
the build**. That is the weaker half: the live failure is the rename or the
delete, not the omission — a badge reading "Proven by the test
`TestRenamedLastMonth`" would otherwise build, ship, and be read aloud as
proof. So `pnpm run check:claims` reads every badge out of the **built HTML**
(via the `data-claim-*` attributes the component emits) and resolves it:

- `measured` → a top-level `func <name>(` must exist under `test/`,
  `internal/` or `api/`. Anchored at line start, so a mention in a comment or
  a call site cannot satisfy it.
- `designed` → `docs/designs/<NN>-*.md` must exist.

It checks **existence, not truth**. It does not run the test, and it cannot
know whether the named test proves the sentence the badge sits beside. `make
test` says the first; a human says the second.

A test name that is not `^[A-Za-z0-9_]+$` is refused before it reaches the
grep. `test=".*"` previously matched the first function in the tree and
certified itself — a claim with no evidence behind it, manufactured by the
gate that exists to prevent exactly that.

### One claim per block

**A badge and its claim must render as one block, and two claims must never
share one.** A retraction's rescue scope is the enclosing block, and an HTML
block is coarser than it looks — a `<div>` of inline pills is one block, and so
is a table. Two claims in one block means either can read as covered by the
other's evidence: the precise failure the claim classes exist to prevent.

The `header` variant renders a `<div>`, so it is always its own block. The
`inline` variant renders a `<span>`, because it has to sit in a sentence —
which means two of them in one paragraph **do** share that paragraph. Nothing
about the component can prevent that without making it unusable inline, so
`check:claims` fails on it instead, using a real HTML parser
(`scripts/block_scope.py`) because "nearest block-level ancestor" is a tree
question and a regex would have to guess at nesting.

`td`, `th` and `tr` are deliberately **not** block boundaries, matching the
gate this mirrors: two claims in one table row are reported, not excused.

The three states are told apart by stroke — filled, hairline, dashed — and by a
mark that survives greyscale. Colour carries none of the meaning. The visible
badge is hidden from the accessibility tree and one complete sentence is
announced in its place, so a screen reader hears
*"Claim class: measured. Proven by the test …"* rather than the fragments the
typography is arranged from.

### `Figure`

`alt`, `width` and `height` are **required**. A generated raster is invisible to
a screen reader and to search without `alt`, and these plates carry
load-bearing information; `width`/`height` are what reserve the box so the page
does not reflow under the reader. A caption is not a substitute for `alt` — the
alt says what the plate shows, the caption says what to take from it.

Wide plates get a **pan viewport, not shrink-to-fit**: the plate keeps a 720px
legibility floor and the frame scrolls. A 1600px plate scaled into a 390px
phone puts its labels near 4px. The frame is `tabindex="0"` so the pan works
without a mouse (SC 2.1.1); the cost is one extra tab stop per figure on wide
screens, which is the cheaper error. A JS zoom widget was rejected — a runtime
dependency on a host with no runtime.

The caption carries the claim class and a `depicts` stamp naming the revision
whose behaviour the plate shows, because a raster cannot be checked against
code that moved underneath it the way a diagram-as-code source could.

**`.webp` only; no `.png` fallback.** WebP has been available in every current
browser engine since 2020. The existing plates in `docs/diagrams/` keep a
`.png` master at ~3.3 MB beside a 1600px `.webp` at ~52 KB — a 60× difference —
so shipping the master as a fallback would cost every visitor a great deal to
serve a share of traffic that is now effectively nil. The `.png` stays a master
in the repository, not a served asset.

## Design system

Ink + Gold, implemented in `shared/src/styles/tokens.css`. Three rules bind:

1. **One gold beat per composition, never interactive.** Each target spends its
   single gold allowance on the 2px rule under the masthead. Nothing else may
   be gold — and nothing interactive ever can be. Gold on off-white measures
   2.33:1, which fails AA for text regardless.
2. **Headings at weight 400.** Weight 500 is for small labels only. The browser
   default is 700, and Starlight sets 600, so both are overridden explicitly.
3. **No shadows, no gradients.** There is no shadow token to reach for.

No webfont ships. `--ax-font-sans` is a geometric stack in the Inter idiom
(Helvetica and Arial deliberately absent), so a visitor sees whichever member
their platform has. **Choosing and self-hosting a typeface is an open
decision** — a scaffold should not settle it.

## What a human must decide and supply before anything deploys

Nothing is deployed, no secret is set and no DNS is pointed. `.github/workflows/web.yml`
builds both targets and uploads two artifacts; its `publish` job is stopped
three separate ways and its last step exits non-zero.

> **Read this before you touch the `web-production` environment.** GitHub
> auto-creates an environment referenced by a workflow file, **with no
> protection rules and no secrets**, the first time that job runs. So if a
> dispatch has ever reached the `publish` job, `web-production` already exists
> and is **unprotected** — finding it there is not evidence that anyone
> configured it. `environment:` is therefore not one of the stops; the armed
> check in step 2 is, because it depends on this repository's state and not on
> the platform's behaviour.

To publish, a human must:

1. **Decide the transport.** Hostinger static apps can be fed more than one
   way. No command in this repository has ever been run against the host, so
   the workflow contains none — a command that has never been executed is a
   guess, and the first person to need it would read it as a guarantee.
2. **Add a required reviewer to the `web-production` environment** — creating
   it first if no dispatch has auto-created it — and only then set the
   repository **variable** `ASSAYD_WEB_PUBLISH_ENABLED` to exactly `true`.
   That variable is the stop that cannot be satisfied by accident: until it is
   set, the job fails on its first step regardless of secrets, environment or
   input. Set it on the **repository**, not the organization — `vars.*` reads
   both, so the same step also refuses to run anywhere but `Quinyte/assayd`,
   and a fork cannot inherit an org-level arming variable.
3. **Set these repository secrets** (the workflow's preflight names each one it
   cannot find):

   | Secret | What it is |
   |---|---|
   | `ASSAYD_WEB_HOST` | hostname to publish to |
   | `ASSAYD_WEB_USER` | account on that host |
   | `ASSAYD_WEB_SSH_KEY` | private key for that account |
   | `ASSAYD_WEB_KNOWN_HOSTS` | pinned host key — without it the first connection trusts whatever answers |
   | `ASSAYD_SITE_PATH` | document root for `assayd.io` |
   | `ASSAYD_DOCS_PATH` | document root for `assayd.dev` |

4. **Point DNS**, and only then set `site:` in both `astro.config.mjs` files.
   It is unset today on purpose: it is the absolute base for canonical URLs and
   the sitemap, and pointing it at a hostname nobody serves would bake a false
   claim into the output. The sitemap integration currently skips for exactly
   this reason.
5. **Replace the stop** in the `publish` job with the transport from step 1.

## Known open items

- **Starlight has no first-party versioned-docs story.** The only option is a
  single-maintainer community plugin that describes itself as in early
  development. Nothing in this scaffold claims versioning works. If versioned
  docs become a requirement, the framework decision should be revisited —
  MkDocs + Material with `mike` is the mature answer.
- No accessibility check and no visual regression check runs in CI. The build,
  the typecheck, the warning gate, the link check and the claim check are the
  gate today.
- The claim check proves a named test **exists**, not that it passes and not
  that it proves the claim beside it. The second of those is a human
  judgement; nothing automates it.
- The page composition is two stacked single-column pages. The components are
  considered; the layout has no second rhythm yet. That is a real gap and it
  belongs with whoever owns the information architecture.
- The link check covers **internal** links and anchors only. External links are
  never fetched, so a link to a page that has since 404'd elsewhere on the web
  will not be caught here. That is deliberate — a third-party outage must not
  be able to fail this repository's CI — and it means external link rot is an
  unguarded axis.
