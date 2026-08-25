# Supply chain

plume's admission rejects agent images that are not cosign-signed (ADR-0019), and design 07 §4 says the platform ships "as a cosign-signed OCI chart, all images pinned by digest, SBOM attached — **the same admission story agents get**." This page is how you check that plume holds itself to it, without taking our word for anything.

## What is published, and where

| Artifact | Location | State at v0.1.0 |
|---|---|---|
| Operator image | `ghcr.io/quinyte/plume-operator` (amd64, arm64) | published, signed, SBOM attested |
| Helm chart | `oci://ghcr.io/quinyte/charts/plume` | **not published** — see below |

Both are published only by `.github/workflows/release.yml`, on a `v*` tag. The
packages inherit the repository's visibility, so while the repo is private they
are private and a pull requires `docker login ghcr.io`.

**v0.1.0 predates the move to the Quinyte organization** and was published under `ghcr.io/ejs-5/plume-operator` at digest `sha256:749ef617444b176c6adeb7e58443bb3abdd65c1d6fd0a856454b912d818a2582`, signed by the workflow identity `https://github.com/ejs-5/plume/`. That artifact is not moved or re-signed: a signature attests to who built what and when, and rewriting history to look tidier would defeat the point. Verify it against the identity it was actually signed with. Everything from v0.1.1 lives under `Quinyte`.

## Signing is keyless, and that is the point

Signatures come from Fulcio and are logged in Rekor, using GitHub's OIDC token — **there is no private key**. Nothing to store, rotate, or leak, and no "someone with the key signed it" to reason about. The signature binds an artifact to *a workflow, in a repository, at a commit*, which is a stronger and more checkable claim.

So the identity you verify against is a workflow, not a person:

```bash
IMAGE=ghcr.io/quinyte/plume-operator
DIGEST=sha256:...          # from `helm show values`, or the release notes

cosign verify "${IMAGE}@${DIGEST}" \
  --certificate-identity-regexp '^https://github.com/Quinyte/plume/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Verification failing is the correct outcome for anything that did not come from that workflow. **Do not relax the identity regexp to make it pass** — a signature that verifies against any identity verifies nothing.

## Read the SBOM before you run it

The SBOM is attached as a signed attestation rather than a release file, because an SBOM nobody can verify is documentation, not evidence:

```bash
cosign verify-attestation --type spdxjson "${IMAGE}@${DIGEST}" \
  --certificate-identity-regexp '^https://github.com/Quinyte/plume/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  | jq -r '.payload | @base64d | fromjson | .predicate.packages[].name'
```

SLSA build provenance is attached to the registry too, and is readable with `gh attestation verify`.

## Digests, not tags

The chart takes `operator.image.digest`, and **the digest wins over the tag when both are set** — the tag is dropped from the rendered reference rather than carried alongside it, because `repo:tag@digest` resolves by digest and the tag then reads as though it mattered.

A tag can be repointed at other content after it was signed. A digest names the content. The release workflow pins the chart to the digest it just published and verified, so `helm install` at defaults runs the artifact that was signed.

```bash
helm install plume oci://ghcr.io/quinyte/charts/plume --version 0.1.0 \
  --set operator.image.digest=sha256:...
```

## What is NOT yet true

Stated plainly, because a supply-chain page that overclaims is worse than none:

- **The chart was not published at v0.1.0.** The release run failed at SLSA provenance *after* pushing and signing the image, and the chart job depends on the image job, so it never ran. The image is real and signed; the chart is not yet in the registry. Fixed for the next tag.
- **There is no SLSA build provenance, and there cannot be one yet.** GitHub's attestation API refuses user-owned **private** repositories outright ("Feature not available for user-owned private repositories"). The step is now conditional on the repo being public, so provenance starts existing the day this repo goes public or moves to an organization — and until then the honest statement is that plume ships a signed image with a verifiable SBOM and *no* build provenance.
- **The chart's default `digest` is empty.** Until a release publishes a chart, `helm install` at defaults resolves by tag. Set the digest explicitly.
- **No `.sig` verification at install time.** Nothing forces a cluster to reject an unsigned plume chart; that is a policy-controller job (Kyverno, or sigstore-policy-controller) and plume does not ship one for itself yet — while it *does* enforce exactly this for agent images.
- **No release has been published.** Everything above describes a workflow that exists and has not run.

## For reviewers

The bar this page claims to meet is the one plume imposes on its users. If any item under "not yet true" would block adoption, say so — the gap is recorded here precisely so it is arguable rather than discovered.

## Running the loop locally

```bash
make e2e          # k3d: create, build, load, helm install, test
DISTRO=kind make e2e
```

**On Colima, raise the inotify instance limit first.** The default is 128, and
every Kubernetes component wants watchers — with more than one k3d or kind
cluster resident, the k3s API server fails to start with `error creating
fsnotify watcher: too many open files`. The cluster is *created*, so the failure
presents as a missing load balancer or an unreachable API, which sends you
looking at the wrong thing:

```bash
colima ssh -- sudo sysctl -w fs.inotify.max_user_instances=8192
colima ssh -- sudo sh -c 'echo fs.inotify.max_user_instances=8192 > /etc/sysctl.d/99-inotify.conf'
```

Give the VM real resources too — `colima start --cpu 8 --memory 16`. This is not
plume-specific; it affects any multi-cluster local Kubernetes work.
