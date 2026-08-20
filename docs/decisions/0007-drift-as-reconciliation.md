# ADR-0007: Drift is a controller concern (conditions + self-healing)
- **Status**: accepted · 2026-08-20
- **Context**: Drift products are dashboards; k8s already has the loop primitive (conditions + controllers).
- **Decision**: Cheap always-on detectors (ontology probes, hourly canary prompts, SLO burn, embedding centroid distance) write conditions (KnowledgeStale, ModelDrifted, Degraded); controllers remediate (re-ingest, pin model/fallback, auto-rollback to last eval-passing rev); the same detectors verify recovery. Semantic health readable in `kubectl get agents`.
- **Consequences**: Detection and remediation ship together; alert fatigue traded for reconciliation.
