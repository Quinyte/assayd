# OTel GenAI semantic conventions — pin reality (2026-08; re-verify at implementation)

- All `gen_ai.*` attributes/spans/metrics remain **Development** stability (formerly "experimental"); no Stable release. https://dev.to/azena-ai/opentelemetrys-genai-semantic-conventions-are-not-stable-yet-heres-what-actually-shipped-in-2026-3mke
- **June 2026 (semantic-conventions v1.42.0)**: all GenAI content deprecated/moved out of the monolithic repo into `open-telemetry/semantic-conventions-genai`, which currently has **no releases, tags, or finalized schema URL** — "pin the version" is not executable as a version number.
- **assayd pin mechanism (design 04 / ADR-0021)**: pin a **commit SHA** of `semantic-conventions-genai` (recorded in the transform + golden-fixture set name); **v1.41** (last monolithic release containing gen_ai) is the frozen fallback baseline. Rename waves absorbed solely in the design-04 transform.
- Supersedes the "pinned semconv version" line in `landscape-2026-08.md` (that wording predates the repo split).
