# OCI artifact facts for design 18 (2026-08)

- **OCI Image/Distribution v1.1 referrers API** finalized 2024: arbitrary artifacts (signatures, SBOMs, attestations, provenance) attach to a subject digest without tag mutation; Harbor/Quay/ECR support landed 2024-25 — older registries fall back to cosign's legacy storage (flag in doctor). https://lesitedefrancois.be/en/security/oci-referrers/
- **cosign v3**: OCI 1.1 referrers discovery + new bundle format are **default** (the old experimental flags deprecated). https://blog.glen-thomas.com/software%20engineering/security/2026/01/20/signing-container-images-with-cosign.html
- **ORAS**: push any artifact with correct media types; `oras discover` lists the full referrer/attestation chain for audit. https://goharbor.io/blog/harbor-as-universal-oci-hub/
