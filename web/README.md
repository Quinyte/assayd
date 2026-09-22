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
pnpm run build        # both targets -> site/dist and docs/dist
pnpm run check        # astro check (typecheck) on both
pnpm run check:links  # internal links + anchors, over the BUILT output
pnpm run dev:site     # local preview of assayd.io
pnpm run dev:docs     # local preview of assayd.dev
```

`check:links` needs `lychee` on PATH (`brew install lychee`; CI pins the
version and its sha256 in `.github/workflows/web.yml`). It runs after a build,
because it checks `dist/`, not source.

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

`--index-files index.html` models the host: `/` resolves because
`dist/index.html` exists, `/guides/` does not because `dist/guides/` holds
`figures.html` and no index. `--index-files ''` was tried and rejected — it
fails the four `href="/"` links in the masthead, because it models a host with
no directory index at all, and directory indexing is a different server
feature from URL rewriting.

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
the build**. Both are mutation-checked: emptying `test=` and replacing a
`Figure` alt with its own filename each stop `pnpm run build` with a non-zero
exit and a named error.

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

To publish, a human must:

1. **Decide the transport.** Hostinger static apps can be fed more than one
   way. No command in this repository has ever been run against the host, so
   the workflow contains none — a command that has never been executed is a
   guess, and the first person to need it would read it as a guarantee.
2. **Create the `web-production` GitHub environment**, and put a required
   reviewer on it. That is where the human gate belongs permanently, rather
   than in an `if:` someone can edit.
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
- `pnpm run build` for the docs target logs a Starlight warning that the `i18n`
  collection is empty. The site is monolingual; the warning is benign.
- No accessibility check and no visual regression check runs in CI. The build,
  the typecheck and the link check are the gate today.
- The link check covers **internal** links and anchors only. External links are
  never fetched, so a link to a page that has since 404'd elsewhere on the web
  will not be caught here. That is deliberate — a third-party outage must not
  be able to fail this repository's CI — and it means external link rot is an
  unguarded axis.
