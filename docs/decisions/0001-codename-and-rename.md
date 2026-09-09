# ADR-0001: Neutral codename "plume"; rename before public release
- **Status**: accepted · 2026-08-20
- **Context**: "graphene" (early drafts) collides with graphene-python (GraphQL) and echoes GrapheneOS. Naming affects module paths, API groups, CLI binary.
- **Decision**: Build under throwaway codename **plume**. Finalize the public name (domain + no major OSS collision) before anything is published; API group and Go module paths are finalized at rename. Track as a pre-release task.
- **Consequences**: Internal docs/paths may say plume; nothing public may.

## Amendment 1 (2026-09-09) — the rename happened; the public name is **assayd**

This ADR's text above is deliberately left in the past tense and still says "plume". It records the decision to build under a throwaway codename, and rewriting it to say "assayd" would make it self-contradictory — a decision to adopt a temporary name cannot name the permanent one. Every other file in the repository was renamed.

- **Name**: **assayd**. An *assay* is the test that establishes whether metal is what it claims before it may be hallmarked, which is this project's thesis: a revision is evaluated, and what was evaluated is what runs, provable by content. The trailing `d` reads as a daemon, in the `containerd`/`linkerd` tradition, and makes the name a coinage rather than a dictionary word.
- **Domains**: `assayd.dev` and `assayd.io`, both registered. `.dev` is authoritative and is the API group.
- **API group**: `assayd.dev/v1alpha1`. **Module**: `github.com/Quinyte/assayd`.

**Why `.dev` is the API group and not `.io`.** The domain here is not a website, it is a string embedded in every CRD, in every object in every user's etcd, and in every RBAC rule and GitOps manifest anyone writes against this project; changing it later is a migration inflicted on adopters. `.io` is the ccTLD for the British Indian Ocean Territory, whose sovereignty passed to Mauritius by treaty in May 2025. As of July 2026 `.io` is fully operational and `IO` remains in ISO 3166-1, so nothing is imminent — the retirement trigger is deletion from that standard, and the closest benchmark, `.su`, outlived its country by 34 years and is still winding down. But "very probably fine, with a five-year forced-migration tail" is a reasonable bet for a marketing site and a poor one for an API group. `.dev` is a permanent gTLD with no country attached. It is also native here rather than exotic: `tekton.dev` and `knative.dev` are CNCF projects, and this project's own dependency is `agentgateway.dev`, so assayd's CRDs sit beside it in every cluster. `.io` is held and redirects.

**How the shortlist was reached, since the constraint was harder than it looks.** Every single-word candidate checked was registered in both TLDs — `assay`, `hallmark`, `sluice`, `airlock`, `tessera`, `signet`, `colophon`, `signum` — including `plume.dev` itself, which is parked on a reseller. Two candidates died on collisions rather than availability: **`provenir`**, the best on meaning, is an existing enterprise software company with registered trademarks in the technology-services class; **`cribra`/`kribra`** are free and uncollided in OSS but sit too close to **Cribl** in an adjacent infrastructure category.

**Owed**: a professional trademark search. `provenir` is the proof that a free domain can sit under an occupied mark, and a domain check is not clearance. Until that search is done, this name is decided but not cleared.

**Not done here, because they are outside the repository**: renaming the GitHub repository to `Quinyte/assayd` (GitHub redirects the old path, so this is safe to do after), and the local working directory.
