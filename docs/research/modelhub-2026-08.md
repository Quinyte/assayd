# ModelHub facts for design 25 (2026-08)

- **KServe RawDeployment mode**: runs without Knative/Istio — the reason binding KServe respects doctrine rule 5 (≤8 pods) and ADR-0012's meshless core. Serverless mode is forbidden in plume; chart pins raw. https://kserve.github.io/website/latest/modelserving/servingruntimes/
- **KitOps ↔ KServe**: documented integration — a `ClusterStorageContainer` registers a storage-initializer image that resolves `kit://` `storageUri`s. Cluster-scoped, **chart-shipped (design 07)**, not per-Model operator output. https://kitops.org/docs/integrations/kserve/
- **Kubeflow Trainer v2**: `TrainJob` API; BuiltinTrainers for SFT/DPO/GRPO; Unsloth/LLaMA-Factory/TRL landing as builtins — pin when released, torchtune/TRL until then. https://blog.kubeflow.org/trainer/intro/
- **Kueue**: GPU queueing/preemption for TrainJobs.
- **agentgateway virtual models** (weighted / conditional / failover routing across real models) shipped in **v1.3.0** — required for design 25's gateway-side weight shifting; postdates the "2.2 CRDs only" wording in ADR-0019(5), folded into the 06-review R2-a minimum-version fix. https://agentgateway.dev/blog/2026-06-17-agentgateway-v1.3.0/
