# ADR-0006: Eval-gated progressive delivery (semantic admission)
- **Status**: accepted · 2026-08-20
- **Context**: 2026 practice runs eval gates as SaaS dashboards + CI scripts + gateway hooks; nobody expresses them as k8s rollout mechanics. Recorded sessions make production traffic a free regression corpus.
- **Decision**: New Agent/Model versions are HELD with zero traffic until their EvalSuite (runner slot: DeepEval/Inspect; datasets = ontology golden set + replayed sessions) clears its gate; pass → gateway-weighted canary → 100%; fail → rollback with the report in CR status + PR. Models use the same gate.
- **Consequences**: The flagship feature; P3 of the build order; first-mover window est. 12–18 months — protect the build order.
