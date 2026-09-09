# Trademark knockout search — "assayd"

**Status**: 2026-09-10. A **knockout** search, not clearance. **Re-verify before publish if more than ~6 months old**, since registers change and a knockout is only as good as its date.

**What this is.** The cheap first pass ADR-0001 Amendment 1 owes: does an obvious prior mark exist that would force a rename? It is **not** the professional clearance that amendment records as owed, and it does not discharge it. Nothing here is legal advice.

**Verdict: no knockout blocker found.** No mark matching "assayd" surfaced in any register searched, and the software namespace is essentially clear. Two real collisions exist and neither is a trademark problem. Read the *Limits* section before treating this as comfort — one of them is material.

## 1. The registers — nothing found, with a caveat that matters

No trademark matching the exact string **assayd** appeared in any search of the US, EU or WIPO registers.

**But I could not query any official register directly.** All three block automated access: the USPTO search API returned `NoSuchKey`, the EUIPO/TMDN TMview API reset the connection, and Justia's trademark search returned `403`. The negative result above therefore comes from **search engines and mirrors that index those registers**, which are neither authoritative nor current. A register-mediated search is exactly what the professional clearance buys, and this is not a substitute for it.

## 2. The `assay*` field is crowded — in biotech, and not where assayd sits

Marks found in the neighbourhood, all owned by laboratory and life-science companies:

| Mark | Owner | Field |
|---|---|---|
| ASSAYPRO, ASSAYMAX, ASSAYLITE | AssayPro LLC | chemical reagents, analysis kits |
| ASSAYQUANT | AssayQuant Technologies Inc. | assay reagents |
| ASSAYMAP | (Agilent-associated) | automated lab equipment, consumables |
| — | Assay Biotechnology Company Inc. | biotech |
| — | Assay, LLC | — |
| — | AssayMetrics (liquidated 2013) | drug-discovery fluorescence instruments |

This is the crowded-field risk worth naming: **`assay` is a term of art in laboratory work, so the register is dense with it.** The density is in goods classes for reagents and instruments, **not** in Nice class 9 (software) or class 42 (SaaS and computer services), which is where assayd would file. Adjacency is not conflict — likelihood of confusion turns on related goods and services — but it is the direction from which an objection would come, and it is what an attorney should be pointed at first.

## 3. The software surface is clear

Checked directly against each registry's API rather than by search:

| Namespace | Result |
|---|---|
| npm | free (404) |
| PyPI | free (404) |
| crates.io | free — "crate `assayd` does not exist" |
| RubyGems | free (404) |
| Go module proxy (`assayd.dev`) | free (404) |
| Artifact Hub (Helm) | 0 packages |
| GitHub org/user `assayd` | **free** (404) |

No software product, project or company named "assayd" was found. The GitHub repositories that match a substring search — `assaydata`, `assaydesign`, `assaydashboard` — are compounds of "assay" plus another word, not uses of this mark.

## 4. Two collisions, neither a trademark issue, one with a practical cost

**Docker Hub username `assayd` is taken.** A personal account, joined 2026-01-16, with **zero repositories** (control-tested: a nonsense username returns 404, this one returns 200 with a user record). A dormant personal handle is not use in commerce and is not a trademark obstacle, but it does mean **assayd cannot publish images as `docker.io/assayd/...`**.

*This costs nothing to route around and the route is better anyway*: publish to **`ghcr.io/quinyte/assayd`**. GHCR is tied to the GitHub org already owned, and keyless cosign signing binds the OIDC identity to the repository — so the signing identity and the image location agree, which they would not if images lived under an unrelated Docker Hub namespace.

**`assayd.com` is registered to a third party** — Cloudflare nameservers, no site served. `assayd.io` and `assayd.dev` are held by this project and parked; `.ai` and `.net` are unregistered. A parked domain serving nothing is not use in commerce either. If the `.com` is wanted it is a purchase, not a registration, and it is not needed: ADR-0001 Amendment 1 already makes `.dev` authoritative and the API group.

## 5. One assumption in ADR-0001 Amendment 1 needs softening

That amendment calls the trailing `d` what "makes the name a coinage rather than a dictionary word". **"Assayd" is also a personal name** — it is the GitHub user `Assayd05` and the Docker Hub account holder's name. That is neutral for trademark purposes, since a personal name is not a mark in classes 9 or 42 unless used in commerce for those services, and it explains why two handles are taken. But the "coined mark" advantage is real and slightly smaller than the amendment implies: coined marks are the strongest and easiest to clear, and this one is coined-*looking* rather than unique.

## Limits — read before relying on this

- **No official register was queried.** Search-engine mediation is the single biggest weakness here and is why this is a knockout rather than clearance.
- **No similarity analysis.** Likelihood of confusion covers phonetic and visual neighbours — ASSAID, ASAD, ASSA, ASSAYED — and this pass searched the literal string. A near-identical mark in class 42 would not necessarily have surfaced.
- **No common-law sweep beyond software.** US rights arise from use without registration; this checked package registries and the web, not trade press, product catalogues or state registrations.
- **Jurisdictions**: US and EU only, by proxy. Nothing checked in UK, India, Japan or elsewhere.

## What follows

1. **This does not unblock a foundation submission.** The Linux Foundation takes assignment of the mark at onboarding and expects a clean one; that still needs the professional clearance ADR-0001 Amendment 1 owes.
2. **It does lower the risk of publishing now** to something ordinary. No obvious prior mark in the relevant classes was found, and a rename remains cheap while adoption is zero.
3. **Use `ghcr.io/quinyte/assayd`** for images and do not plan on the Docker Hub namespace.
4. `provenir` remains the cautionary case from this project's own shortlist: a free domain sitting under an occupied mark. A knockout that finds nothing is evidence, not proof.
