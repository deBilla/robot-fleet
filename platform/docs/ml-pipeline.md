# FleetOS ML Pipeline

This document maps the six stages of the ML lifecycle to the FleetOS codebase, identifies gaps, and proposes next steps.

Why the walking policy doesn't walk yet, the experiments run so far, current research and the plan: [locomotion-research.md](locomotion-research.md).

```
Problem Framing → High-Level Design → Data & Features → Modelling → Inference & Evaluation → Deep Dives
```

---

## 1. Problem Framing

**Goal:** Teach humanoid robots locomotion, reaching, and grasping in a 10×10 m simulated lab.

| Aspect | Detail |
|--------|--------|
| Task | Forward locomotion — walk without falling |
| Robot | MuJoCo humanoid, 25 actuators (abdomen, legs, arms, grippers) |
| Observation | 86-dim unified vector: joint pos/vel (50), root quat/vel (10), hand positions (6), grippers (4), objects (12), task encoding (4) |
| Action | 25-dim position targets (joint angles in radians, PD-controlled) |
| Reward | `forward_velocity + 1.0 − 0.1 * control_cost` (locomotion); sparse -1/0 for HER (reach, grasp) |
| Termination | Torso height < 1.0 m (fallen; a crouch counts — at 0.3 m PPO learned to crawl at 0.57 m instead of balancing) |
| Episode length | Max 1000 steps |
| Physics | 0.003 s timestep, 7 substeps per control tick, RK4 integrator |

**Key files:**
- `platform/training/fleetos_env.py` — locomotion environment (`FleetOS-Humanoid-v2`)
- `platform/training/fleetos_reach_env.py` — reaching environment (`FleetOS-Reach-v1`)
- `platform/training/fleetos_grasp_env.py` — grasping environment (`FleetOS-Grasp-v1`)
- `platform/training/observation.py` — unified 86-dim observation builder
- `platform/training/assets/lab_room.xml` — MuJoCo world (walls, tables, objects, humanoid with grippers)

**Coverage: Complete.** Locomotion reward is well-shaped. Reach and grasp use sparse rewards (compatible with HER). Task encoding enables multi-task policies.

---

## 2. High-Level Design

FleetOS uses a **cloud-brain** architecture — all reasoning on the platform, robots handle execution only.

```
┌─ Playground (Robot) ──────────────────────────────┐
│  MuJoCo Simulator (50 Hz)                         │
│    ├─ Telemetry Client → gRPC → Ingestion         │
│    │    (incl. training context: obs, action, …)  │
│    └─ Command Executor ← gRPC ← Ingestion         │
└───────────────────────────────────────────────────┘
        │                              ▲
        ▼                              │
┌─ Platform (Cloud Brain) ──────────────────────────┐
│                                                    │
│  Ingestion → Kafka → Processor                     │
│                        ├─ S3 raw archive           │
│                        ├─ S3 training_data/v3/     │
│                        ├─ Redis hot state          │
│                        └─ Postgres cold metadata   │
│                                                    │
│  Training (Temporal TrainingPipelineWorkflow)      │
│    ├─ Snapshot fleet experience (dataset manifest) │
│    ├─ [Katib HPO: TPE, 4 trials × 2 parallel]      │
│    ├─ Train offline (CQL-SAC, no simulator):       │
│    │    Argo workflow or Kubeflow TrainJob         │
│    ├─ Evaluate on MuJoCo test track → eval gate    │
│    └─ Register (staged) → [auto canary deploy]     │
│         ↓                                          │
│  Model Registry (staged → canary → deployed)       │
│         ↓                                          │
│  Inference Service (:8081)                         │
│    ├─ /predict — PPO/SAC policy forward pass       │
│    ├─ /resolve — FAISS semantic command search     │
│    └─ Hot-swap via Redis pub/sub                   │
│                                                    │
│  Temporal Orchestration                            │
│    ├─ ModelDeploymentWorkflow (canary 5→25→50→100) │
│    └─ CommandDispatchWorkflow                      │
│                                                    │
│  Analytics (Spark)                                 │
│    ├─ Episode extraction (daily)                   │
│    └─ Fleet telemetry aggregation (hourly)         │
└────────────────────────────────────────────────────┘
```

**Key files:**
- `platform/internal/temporal/workflows/training_pipeline.go` — training pipeline (dataset snapshot → [Katib] → offline train → test-track evaluate → gate → register → canary)
- `platform/internal/experience/` — fleet experience record format + S3 keys; `platform/internal/temporal/activities/training_dataset.go` — dataset snapshots (manifests)
- `platform/training/train_offline.py` — offline CQL-SAC trainer (reads a dataset manifest; no simulator)
- `platform/internal/kubeflow/` — Kubeflow Trainer TrainJob + Katib Experiment builders; `platform/deploy/k8s/workflows/` — Argo WorkflowTemplates
- `platform/training/pipeline.py` — Kubeflow *Pipelines* (KFP) DAG definition; KFP is not installed and nothing invokes it (kept as reference)
- `platform/internal/temporal/workflows/deployment.go` — canary deployment workflow
- `platform/inference/server.py` — model serving + semantic resolution

**Coverage: Complete.** Clear separation of concerns, well-orchestrated pipeline with progressive rollout.

---

## 3. Data and Features

### 3.1 What exists

#### Data sources

| Source | Rate | Format | Storage | Key file |
|--------|------|--------|---------|----------|
| Telemetry | 10 Hz per robot | Protobuf → JSON | S3 `telemetry/{date}/{hour}/{robot_id}/` | `playground/simulator/telemetry_client.py` |
| Fleet experience (v3) | 10 Hz per robot (+ terminal record on each fall) | NDJSON `{obs (86), action (25), reward, episode_done, episode_id, step, exploration, …}` | S3 `fleetos-telemetry/training_data/v3/{YYYY/MM/DD/HH}/{robot_id}/` (100 per batch) | simulator `main.py` / `transitions.py` → `platform/internal/experience/` (processor) |
| Robot hot state | 10 Hz | JSON | Redis `robot:state:{robot_id}` (5 min TTL) | `platform/cmd/processor/main.go` |
| Robot cold metadata | ~0.2 Hz (every 50th packet) | SQL row | Postgres `robots` table | `platform/internal/store/postgres.go` |
| Episodes | Daily batch | JSON | S3 `episodes/{date}/{robot_id}/` | `platform/analytics/episode_extractor.py` |
| Fleet analytics | Hourly batch | JSON + ClickHouse | S3 + `robot_telemetry_hourly` | `platform/analytics/telemetry_analytics.py` |

Observations are **egocentric** (observation version 3): joint state, torso orientation without yaw, velocities in the heading/body frame, and hand/object positions relative to the torso in the heading frame. Version 2 used world-frame hand/object positions, so a policy only behaved like on the test track when the robot stood at the origin with objects in their start spots — fleet robots don't. v2 experience (`training_data/v2/`) and v2 policies are not readable by v3 code: the experience prefix moved to `training_data/v3/`, and every policy now ships `policy_meta.json` with its `obs_version`; robots, inference and evaluation refuse mismatches instead of executing them.

#### Observation space (86 dims — unified format)

```python
obs = concatenate([
    joint_pos,         # 25-dim — all actuator angles
    joint_vel,         # 25-dim — all actuator velocities
    root_quat,         # 4-dim  — torso orientation quaternion
    root_linvel,       # 3-dim  — torso linear velocity
    root_angvel,       # 3-dim  — torso angular velocity
    hand_pos_right,    # 3-dim  — right hand site position
    hand_pos_left,     # 3-dim  — left hand site position
    gripper_pos,       # 4-dim  — gripper joint angles
    object_positions,  # 6-dim  — red_box + blue_cylinder xyz
    object_velocities, # 6-dim  — object velocities
    task_encoding,     # 4-dim  — one-hot [locomotion, reach, grasp, idle]
])
```

Single source of truth: `platform/training/observation.py`. Used by all environments, inference, and fleet experience (the simulator sends the exact observation the policy saw).

#### Data pipeline

```
Simulator (50 Hz) → gRPC Telemetry (+ training context) → Ingestion → Kafka → Processor
                                                           ├─ S3 training_data/v3/ (fleet experience)
                                                           ├─ S3 raw archive
                                                           ├─ Redis (real-time)
                                                           └─ Postgres (metadata)
```

### 3.2 Gaps

| Gap | Impact | Suggested approach |
|-----|--------|-------------------|
| **No feature versioning** | Observation changes break model compatibility silently | Add obs-space hash to model registry; validate at inference load time |
| **No data quality monitoring** | Sensor drift or sim changes go unnoticed | Track obs distribution stats in Prometheus; alert on KL divergence |
| **No curriculum data** | All experience is from uniform environment | Stage environment configs (flat → obstacles → wind) tied to training phases |
| **Fleet data is only as good as fleet behaviour** | Offline RL can't get far beyond the behaviour policy; today's fleet mostly idles/falls (measured: 0% test-track pass rate after 29,480 transitions) | Bootstrap a walking policy with manual online training; raise exploration; collect task-diverse data |
| **Snapshots list the whole prefix** | Fine locally; slow at fleet scale | Date-range / per-robot filtering and sharded manifests |

---

## 4. Modelling

### 4.1 What exists

| Aspect | Detail | File |
|--------|--------|------|
| PPO (locomotion) — manual / bootstrap | On-policy RL in MuJoCo, MLP [256, 256], lr=3e-4, n_steps=2048, gamma=0.99 | `training/train_locomotion.py` |
| SAC+HER (reaching) | Off-policy RL with hindsight goal relabeling, sparse reward | `training/train_reach.py` |
| SAC+HER (grasping) | Goal-conditioned grasp+lift with optional scripted demos | `training/train_grasp.py` |
| CQL-SAC (offline RL) — **the pipeline's trainer** | Conservative Q-Learning on a pinned snapshot of fleet experience; raw observations (no VecNormalize), each joint's full control range as the action space (fleet actions scaled into SAC's [−1, 1] when loaded), no simulator | `training/train_offline.py` |
| Behavior Cloning | Supervised pre-training on (obs, action) pairs, SB3-compatible output | `training/train_bc.py` |
| Autoencoder | Unsupervised 86→32-dim representation learning on observations | `training/train_autoencoder.py` |
| Normalisation | VecNormalize (obs + reward), clip_obs=10.0 | `train_locomotion.py` |
| Fine-tuning | `--base-model` loads prior policy (including BC-pretrained) | Online scripts, standalone (not in the pipeline) |
| Export | `.zip` (SB3) + `.onnx` (PyTorch, locomotion only) | `train_locomotion.py` |
| Training infra | Argo WorkflowTemplates or Kubeflow Trainer TrainJobs (runtime `fleetos-sb3`, 2–4 CPU / 3–6 Gi); CPU-only on local k3s | `deploy/k8s/workflows/`, `internal/kubeflow/` |
| Hyperparameter search | Katib Experiment (TPE default) over learning_rate, cql_alpha, batch_size; each trial = offline training on the snapshot + 5 test-track episodes → best params train the final model | `internal/kubeflow/`, `train_offline.py` flags |

### 4.2 Training Paradigms

| Paradigm | Scripts | Data Source | When to Use |
|----------|---------|-------------|-------------|
| **Offline RL** (pipeline) | `train_offline.py` | Fleet experience (`training_data/v3/`) | Default — learn from what robots actually did |
| **Online RL** (manual) | `train_locomotion.py`, `train_reach.py`, `train_grasp.py` | Simulator interaction | Bootstrapping a first policy that produces useful fleet data |
| **Supervised (BC)** | `train_bc.py` | Filtered experience or expert rollouts | Pre-training before RL fine-tuning for faster convergence |
| **Unsupervised** | `train_autoencoder.py` | S3 observations only | Anomaly detection, representation learning |
| **BC → RL** | `train_bc.py` then `train_*.py --base-model` | Demos then simulator | Complex tasks like grasping — best overall approach |

### 4.3 Remaining Gaps

| Gap | Impact | Suggested approach |
|-----|--------|-------------------|
| **Small HPO budget** | Katib defaults to 4 trials × 2,000 offline updates + 5 test-track episodes — enough to exercise the loop, not to find good parameters | Raise `kubeflow.hpo.maxTrials` / `trialGradientSteps` / `evalEpisodes` on real hardware; add net_arch to the search space |
| **Offline updates are heavier, not lighter** | Measured: same 3,200 gradient updates took 51 s online (PPO) vs 100 s offline (CQL) on a 2–4 CPU pod | Benefits are data reuse, lineage and no sim-to-real gap — budget compute accordingly (GPU for CQL at scale) |
| ~~**Inference ignores VecNormalize for PPO models**~~ ✅ fixed | Robots (edge) and the inference service now apply each model's `vec_normalize.pkl` when present | — |
| **Offline transitions are 10 Hz samples of a 47.6 Hz controller** | With edge execution the action changes every control tick, but records sample it every telemetry interval | Record at control rate (batched) or execute offline models with matching action repeat |
| **Rewards come from the simulator** | Real robots don't compute `forward_vel + alive - ctrl_cost` | Derive rewards from telemetry in the processor |
| **No experiment tracking** | Can't compare runs side-by-side | Integrate MLflow or W&B; log to existing S3 backend |
| **No curriculum learning** | Policy trains on single difficulty | Progressive env configs: gravity, friction, wind ramp over training |
| **Fixed architecture** | MLP may underperform for high-dim obs | Try LSTM/attention for temporal dependencies in joint dynamics |
| **Autoencoder not integrated** | Encoder exists but not plugged into RL as feature extractor | Write custom SB3 `FeaturesExtractor` using `encoder.pt` |

---

## 5. Inference and Evaluation

### 5.1 Inference (strong)

| Capability | Detail | File |
|------------|--------|------|
| Serving | HTTP `/predict` endpoint, 17-joint torque output | `platform/inference/server.py` |
| Semantic commands | FAISS vector search + verb matching on `/resolve` | `platform/inference/vector_search.py` |
| Model hot-swap | Redis `model:deployed` event → S3 download → atomic policy swap | `server.py` lines 303–357 |
| Thread safety | `_policy_lock` for concurrent predict calls | Same |
| Fallback | `_StubPolicy` (zero actions) if model unavailable | Same |
| Latency | Tracked via `fleetos_inference_duration_seconds` Prometheus histogram | `platform/internal/middleware/metrics.go` |

### 5.2 Evaluation (partial)

#### What exists

| Evaluation type | Detail | File |
|-----------------|--------|------|
| Post-training eval | 20 episodes, mean/std reward | `train_locomotion.py` lines 160–185 |
| Scenario robustness | 5 presets applied to the test-track physics (`FleetOSHumanoidEnv.set_conditions`): standard, low_friction (sliding friction ×0.5), high_gravity (−11 m/s²), windy (5 N side push on the torso), combined | `platform/training/evaluate_policy.py` |
| Pass gate | reward ≥ 500 AND steps ≥ 100 per scenario | Same |
| Canary metrics | Live `success_rate` comparison, 10% degradation → rollback | `platform/internal/service/deployment_service.go` |
| Per-robot gauges | Prometheus: reward, uptime_pct, fall_count, battery | `platform/internal/middleware/metrics.go` |

#### Gaps

| Gap | Impact | Suggested approach |
|-----|--------|-------------------|
| **No online A/B dashboard** | Can't visualise canary vs baseline in real time | Grafana panel: canary model reward vs baseline over time |
| **No statistical significance** | 20 eval episodes is low-N; results may be noisy | Bootstrap CI on reward; require p < 0.05 before promotion |
| **No per-behaviour metrics** | Only aggregate reward; can't tell if gait quality or energy degraded | Decompose: forward velocity, control cost, fall rate, stride length |
| **No automated regression suite** | Manual scenario selection | CI job that runs all scenarios on every model before staging |

---

## 6. Deep Dives

This stage is the least covered. It focuses on understanding *why* the policy behaves as it does and pushing its limits.

### 6.1 Not yet implemented

| Deep dive | Purpose | Approach |
|-----------|---------|----------|
| **Failure analysis** | Understand why and when robots fall | Cluster fall episodes by joint torque / velocity patterns at failure timestep. Identify common failure modes (e.g., knee lock-up, asymmetric gait). |
| **Reward decomposition** | Detect reward hacking or imbalanced components | Log forward_velocity, alive_bonus, control_cost separately during eval. Plot component trajectories per episode. |
| **Sim-to-real gap analysis** | Assess transferability to real hardware | Add domain randomisation to `fleetos_env.py` (friction ±30%, mass ±10%, action latency 10–50 ms). Evaluate policy robustness. |
| **Policy interpretability** | Identify which observations drive decisions | SHAP values or gradient saliency on the MLP. Rank the 376 observation dims by importance. Prune unused dims. |
| **Adversarial robustness** | Test policy under perturbation | Inject Gaussian noise into observations during eval. Measure graceful degradation vs. catastrophic failure. |
| **Energy efficiency** | Minimise actuator usage for same gait | Add energy term to reward. Generate Pareto front: forward speed vs. energy consumption. |
| **Gait analysis** | Characterise learned locomotion style | Extract joint angle trajectories over gait cycles. Compare to reference human walking data. Compute symmetry index. |

### 6.2 Existing infrastructure that supports deep dives

- **S3 episode archive** — raw data for post-hoc analysis is already stored
- **Prometheus per-robot metrics** — fall_count, reward, uptime are tracked
- **Spark pipeline** — episode extractor already segments telemetry into episodes
- **ONNX export** — enables running policy in non-Python tools for analysis

---

## Coverage Summary

| Stage | Coverage | Primary gap |
|-------|----------|-------------|
| Problem Framing | ~100% | — |
| High-Level Design | ~100% | — |
| Data and Features | ~70% | No feature versioning or data quality monitoring |
| Modelling | ~80% | Small HPO budget, no experiment tracking; fleet data limited by current fleet behaviour |
| Inference and Evaluation | ~75% | No online A/B dashboard, weak statistical evaluation |
| Deep Dives | ~5% | No failure analysis, interpretability, or robustness testing |

### What was added (Modelling improvements)

- **SAC+HER for reaching** — goal-conditioned reaching with sparse rewards and hindsight relabeling
- **SAC+HER for grasping** — goal-conditioned grasp+lift environment with scripted demo generation
- **Offline training from fleet experience** — the pipeline trains CQL-SAC on pinned snapshots of real fleet data (`training_data/v3/`), no simulator in training; MuJoCo is the evaluation test track; models record their dataset manifest
- **Behavior cloning** — supervised pre-training on (obs, action) pairs, SB3-compatible output for fine-tuning
- **Autoencoder** — unsupervised 86→32-dim representation learning on observation vectors
- **Unified 86-dim observation** — single format across all tasks and training scripts

---

## Recommended Next Steps (Priority Order)

1. **Experiment tracking** — Integrate MLflow backed by existing MinIO. Enables comparing algorithms with proper run history.

2. **Better fleet data** — Bootstrap a walking policy (manual online PPO) so fleet experience contains useful behaviour; tune exploration; then scale the Katib budget (`use_katib: true` already searches lr / cql_alpha / batch size).

3. **Autoencoder as feature extractor** — Plug trained encoder into SB3 as custom `FeaturesExtractor` for downstream RL tasks.

4. **Feature versioning** — Hash the observation space config and store alongside model in registry. Validate at inference load.

5. **Simulator canary routing** — Inference already serves per-request artifacts (LRU cache, PPO+SAC); route simulator calls through each robot's assigned model too.

6. **Automated BC→RL pipeline** — Temporal workflow that chains behavior cloning pre-training with RL fine-tuning as a single pipeline.

7. **Failure analysis pipeline** — Spark job that clusters fall episodes and produces a weekly failure mode report.

8. **Online A/B dashboard** — Grafana panel showing canary vs baseline reward, fall rate, and battery drain in real time during rollouts.
