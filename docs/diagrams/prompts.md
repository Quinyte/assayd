# Diagram prompts (for regeneration / amendment)

All three rendered with `nano_banana_pro` under the visualizer Ink + Gold design system
(`plugins/visualizer/shared/design/design-system.md`). Palette discipline: ink `#14141A`
carries the composition, gold `#C8A24B` appears exactly once per plate, grey-ink `#2A2A33`
for annotations, paper `#FAFAF7` ground. Typography: one geometric sans (Inter-like, weight 400).

Regenerate via the visualizer CLI (needs an unblocked `GEMINI_API_KEY`):

    bun run <plugin>/shared/tools/generate-image.ts --model nano-banana-pro \
      --prompt "<prompt below>" --size 2K --aspect-ratio <ratio> --output <file>.png

or via the higgsfield MCP `generate_image` with `{"model":"nano_banana_pro"}` — the route
used for this set, because the visualizer's Gemini key returned `API_KEY_SERVICE_BLOCKED`.

## 01 — Architecture (16:9)
Three horizontal bands. Band 1 CONTROL PLANE (faint `#F3ECD9` wash): Git repo → Argo CD →
assayd operators. Band 2 DATA PLANE: Agent A / Agent B → **gold-outlined agentgateway
(A2A / MCP / LLM)** → MCP tools / Knowledge Graph / LLM providers. Band 3 SUBSTRATE:
NATS JetStream, Postgres, with an arrow down from the gateway into NATS.
Gold beat: the agentgateway box.

## 02 — Eval-gated rollout (16:9)
Left-to-right flow: git commit → candidate revision → HELD — no traffic → eval job
(annotated *golden set + replayed sessions*) → **gold decision diamond `gate`** →
pass ↗ canary 10% → full traffic; fail ↘ rollback + report.
Gold beat: the gate diamond.

## 03 — On-behalf-of sequence (4:3)
Five lifelines: User, agentgateway, IdP, Agent, MCP Tool. Messages: request + user token →
**gold: exchange RFC 8693 → gold: agent-scoped token** → A2A task → tool call →
call + scoped token → result.
Gold beat: the exchange arrow pair.

## Validation checklist applied to each
label accuracy and spelling · exactly one gold beat · flat colors, no gradient/shadow/3D ·
grid alignment · legibility at 100% · structure matches the design docs it depicts.

## 04 — KG ingestion (16:9)
acquire → normalize → extract → resolve → **gold `Gate A` diamond** → write → ink `Gate B` diamond →
Ready; both gates drop `fail` arrows into a single `quarantine` box. Annotations beneath boxes:
chunk/dedupe/redact · ontology-typed · extraction quality · ontology invariants · probes pass.
Gold beat: Gate A (the before-any-write gate).

## 05 — Governance ring (1:1)
Large **gold dashed rounded-square boundary** labelled ENFORCED AT THE GATEWAY; inside, a four-box
clockwise cycle assemble → act → observe → stop-check with centre italic caption "the agent loop /
your code"; outside each edge, one label: token + cost budget · hop limit / cycle detect ·
approval interrupt · kill switch. Gold beat: the boundary.

## 06 — API surface (1:1)
Hub and spoke. Centre **gold `Agent` box** ("any A2A container"); App top-left, KnowledgeGraph
top-right, Workflow bottom-left, EvalSuite bottom-right, Model bottom-centre. Exactly five arrows with
explicit destinations: App→Agent `members` · Agent→KnowledgeGraph `knowledge` · Agent→EvalSuite `gates` ·
Workflow→Agent `steps` · Model→EvalSuite `gates`. Gold beat: Agent.
*Lesson: state arrowhead destinations explicitly — v1 reversed one and mis-targeted another.*

## 07 — What actually runs (16:9)
Three chip-tray bands. CORE (gold-tint wash, "8 pods"): **gold `agent-operator` chip** captioned
"the only assayd code", then agentgateway · SPIRE · Zitadel · NATS · Postgres · OpenObserve.
PLUS: workflow-operator · workflow-runtime · OpenFGA · model-operator · Argo.
ENTERPRISE: tenant-operator · compliance packs. Footer: "two stateful dependencies: Postgres and NATS".
Gold beat: agent-operator.

## 08 — Agent rollout lifecycle (16:9)
Rounded state boxes: Pending → **gold `Held`** → Canary → Ready along the top; Degraded and Killed below.
Exactly six arrows: registered · gates pass · weights 100% · SLO burn · rollback · kill switch.
Caption under Held: "no traffic until evals pass". Gold beat: Held.
*Lesson: constrain with "each label appears exactly once" — v1 duplicated and misspelled a transition label.*

---

# Website plates (the audit's replacement set)

The plates below are specified by `AUDIT-2026-09.md` (Job 2) and placed on the website with
`web/shared`'s `Figure`. Each section holds the **exact prompt**, the gold beat, and the
**sources of truth** a regeneration must re-read first: a `.webp` cannot be diffed against the
code, so this list and the page's `depicts` stamp are what stand in for that diff.

**Transport.** The visualizer's `generate-image.ts` reads `GEMINI_API_KEY`, which on 2026-09-23
returned `403 API_KEY_SERVICE_BLOCKED` for every Nano Banana model. The human has authorised a
second transport for the same model family: OpenRouter's chat-completions endpoint with
`modalities: ["image","text"]`, `image_config: {aspect_ratio, image_size: "2K"}` and model
`google/gemini-3-pro-image` (Nano Banana Pro, checked against OpenRouter's model list on
2026-09-23). Only the transport differs; prompts, design system and gold-beat rule are the
visualizer's. **Neither route produced these plates yet** — see each section's status.

## P1 — What a default helm install runs (16:9) — status: owed

Replaces `07-what-runs`. Gold beat: the `assayd-agent-operator` box. Figure claim: `measured`,
`TestADefaultInstallRunsOneOperatorAndNothingElse` (`test/chart/whatruns_test.go`, written to pin
this plate's inventory). Placed on assayd.dev `/what-runs-today.html`.

Sources of truth: `charts/assayd/templates/{operator,admission,rbac,pdb,namespace}.yaml`,
`charts/assayd/values.yaml`, `charts/assayd/Chart.yaml`, `charts/assayd/crds/`,
`test/chart/whatruns_test.go`, `hack/e2e.sh` (`GWAPI_VERSION`, `AGW_VERSION`, the Gateway block).

Re-verified against `main` `a1ce0bb` by rendering the chart: one Deployment at two replicas; four
ValidatingAdmissionPolicies and four bindings; no webhook, no Service; `gateway.enabled=true` adds
two Roles and two RoleBindings; `tier: plus` fails the render. Nothing in the audit's P1 spec was
stale.

```text
Precise vector technical diagram in professional engineering drafting style, 16:9, flat.

BACKGROUND: warm off-white paper #FAFAF7, flat, no texture. No shadows, no gradients, no 3D, no icons, no logos, no watermark, no signature.

TYPOGRAPHY: one geometric sans-serif similar to Inter, weight 400 everywhere (never bold), slightly editorial, generous tracking on the title. All text ink #14141A unless stated. Every word spelled exactly as given below, nothing added.

TITLE (top left, all caps, large, weight 400): WHAT A DEFAULT HELM INSTALL RUNS
Top right corner, small ink label: "● measured"

LAYOUT: two clearly separated zones side by side below the title, left zone wider (about 60 percent), right zone about 40 percent.

LEFT ZONE — small all-caps zone heading in ink: SHIPPED BY THE CHART
Inside, top: one large rectangle with an outline in gold #C8A24B (2px), white-ish fill #FAFAF7, containing three lines of text:
  line 1 (larger): assayd-agent-operator
  line 2: Deployment · 2 pods
  line 3 (small italic, ink-soft #2A2A33): the only assayd code in the cluster
Below it, a row of four equal small rectangles with thin 1px ink #14141A outlines, labelled in order:
  assayd-namespace-labels
  assayd-gateway-routes
  assayd-gateway-policies
  assayd-api-keys
Under that row one small caption in ink-soft italic: ValidatingAdmissionPolicy (CEL) · no webhook
Below, one medium rectangle with a 1px ink outline labelled:
  line 1: Agent
  line 2 (small italic, ink-soft): the only CRD · agents.assayd.dev

RIGHT ZONE — a flat rectangular wash in pale gold-tint #F3ECD9 with square corners. Small all-caps heading inside it in ink: INSTALLED BY THE TEST HARNESS, NOT BY ASSAYD
Inside, three stacked rectangles with 1px ink outlines and #FAFAF7 fill, labelled:
  Gateway API v1.6.0
  agentgateway 1.5.0
  Gateway · gatewayClassName: agentgateway

There are NO arrows and NO connecting lines anywhere in the diagram.

FOOTER (across the bottom, one line of small ink-soft #2A2A33 text, regular, not italic):
Not installed: SPIRE, Zitadel, NATS, Postgres, OpenObserve. tier: plus fails the render.

COLOR RULES: ink #14141A for all linework and text; ink-soft #2A2A33 for captions; gold #C8A24B appears on exactly ONE element in the whole image — the outline of the assayd-agent-operator rectangle — and nowhere else (no gold text, no gold lines, no gold dots). Pale gold-tint #F3ECD9 only as the right zone's background wash. No other colors.

Grid-aligned, generous whitespace, crisp 1px hairlines, professional engineering documentation quality, every label legible.
```

## P3 — The auth transaction (16:9) — status: owed

New subject. Gold beat: the `ProbingAfter` box. Figure claim: `measured`,
`TestCreatePublishesOnlyOnA401`. Placed on assayd.io `/concepts/the-auth-transaction.html`.

Sources of truth: `internal/controller/authtxn.go` (the `Stage*` and `Tx*` constants, `authStep`,
`enterLock`, `lockMissingPolicy`, `recreateRoute`, `refuseAdopt`, `runCreate`, `runLock`),
`api/v1alpha1/agent_types.go` (`AuthTransaction`), `internal/controller/authabove.go`,
`test/envtest/authcreate_test.go`, `test/envtest/authlock_test.go`, `test/e2e/lock_test.go`.

**One correction to the audit's sketch.** `AUDIT-2026-09.md` P3 draws a single left-to-right chain
`PreparingRoute → ProbingBefore → ApplyingPolicies → …`. No transaction takes that edge: a `Create`
goes `PreparingRoute → ApplyingPolicies` (`runCreate`), and only a J2 or K2 `Lock` enters at
`ProbingBefore` (`enterLock`) and leaves it for `ApplyingPolicies` (`runLock`). The prompt below
draws the two as parallel entries into `ApplyingPolicies`, and adds the edge the sketch left out:
a `Lock` goes from `ProbingAfter` straight to `Served` (`lockServed`) and never through
`Publishing`. Re-verified against `main` `a1ce0bb`; design 03 A84 and PR #65 (A85) touch
`authserved.go`, not the stages.

```text
Precise vector technical diagram in professional engineering drafting style: a state diagram drawn as boxes and labelled arrows, 16:9, flat.

BACKGROUND: warm off-white paper #FAFAF7, flat, no texture. No shadows, no gradients, no 3D, no icons, no logos, no watermark, no signature.

TYPOGRAPHY: one geometric sans-serif similar to Inter, weight 400 everywhere (never bold), slightly editorial. All text ink #14141A; annotations small italic ink-soft #2A2A33. Every word spelled exactly as given, each label appears exactly once, nothing added.

TITLE (top left, all caps, large, weight 400): THE AUTH TRANSACTION
Subtitle under it, small italic ink-soft: stages as the operator defines them — status.auth.transaction.stage
Top right corner, small ink label: "● measured"

MAIN ROW (left to right across the middle, rectangles with 1.5px ink #14141A outlines, #FAFAF7 fill, each with a name line and one small italic ink-soft note line):
  Column 1 holds TWO boxes stacked vertically, one above the other, NOT connected to each other:
    upper box: PreparingRoute — note: route written with no backendRefs
    lower box: ProbingBefore — note: one anonymous request, an observation
  Column 2: ApplyingPolicies — note: writes <agent>-auth
  Column 3: Converging — note: route and policy report converged
  Column 4: ProbingAfter — note: waits for a 401 it may credit.  THIS BOX ONLY has a 2px GOLD #C8A24B outline.
  Column 5: Publishing — note: backendRefs put on the route
  Column 6: Served — note: no transaction in the slot; status.auth.mode recorded.  Draw this box with a heavier 2.5px ink outline.

ARROWS (solid ink, 1.5px, clear arrowheads):
  - PreparingRoute → ApplyingPolicies
  - ProbingBefore → ApplyingPolicies
  - ApplyingPolicies → Converging
  - Converging → ProbingAfter
  - ProbingAfter → Publishing, labelled small: Create
  - Publishing → Served
  - one arrow from ProbingAfter curving ABOVE Publishing directly into Served, labelled small: Lock — its route already serves
  - one return arrow from ProbingAfter curving BELOW back into Converging, labelled small italic: NACK Event on the Gateway
  - a small self-loop on top of ProbingAfter, labelled small italic: hold: GatewayAuthPolicy

ENTRY ARROWS (thinner 1px ink arrows coming up from a labelled start point below the row, each label in small ink text):
  - "Create · apikey" → into PreparingRoute
  - "Lock · J2 / K2" → into ProbingBefore
  - "Lock · missing policy" → into ApplyingPolicies
  - "Create · auth: none" → a long thin arrow running along the bottom, under the row, rising into Publishing, with small italic note: skips every earlier stage

SEPARATE BOX (bottom right corner, unconnected to anything, 1px ink outline): Refused — note: an Adopt only ever sits here

COLOR RULES: ink #14141A for all linework and text; ink-soft #2A2A33 for notes; gold #C8A24B on exactly ONE element in the whole image — the outline of the ProbingAfter box — and nowhere else (no gold text, no gold arrows). No washes, no other colors.

Grid-aligned, generous whitespace, crisp lines, professional engineering documentation quality, every label legible.
```
