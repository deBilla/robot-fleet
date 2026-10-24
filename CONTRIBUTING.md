# Contributing to FleetOS

Thanks for your interest. FleetOS is a robot-fleet platform where simulated MuJoCo
humanoids stream telemetry to a cloud "brain" (Go services, Kafka, Temporal) that trains
walking policies from the fleet's own experience and rolls them back out through a gated
canary. There is work here for Go backend, Python/RL, Kubernetes/MLOps and frontend people.

New here? Start with an issue labelled
[`good first issue`](https://github.com/deBilla/robot-fleet/labels/good%20first%20issue), or
pick something from [`help wanted`](https://github.com/deBilla/robot-fleet/labels/help%20wanted).
Comment on the issue before starting so two people don't build the same thing.

## Where things live

| Area | Path | Language |
|------|------|----------|
| Platform services (api, ingestion, processor, worker, CLI) | `platform/cmd/`, `platform/internal/` | Go |
| Training: offline CQL from fleet data, PPO bootstrap, test-track evaluation | `platform/training/` | Python (SB3, MuJoCo) |
| Inference service | `platform/inference/` | Python |
| Robot simulator (physics, telemetry, on-robot policy execution) | `playground/simulator/` | Python (MuJoCo) |
| Dashboards | `playground/web/`, `platform/admin-web/` | React + TypeScript |
| Deployment (Helm, Argo, Kubeflow, Terraform) | `platform/deploy/`, `platform/scripts/` | YAML, shell |
| Design docs | `platform/docs/` (`architecture.md`, `ml-pipeline.md`) | Markdown |

[`CLAUDE.md`](CLAUDE.md) is the detailed engineering guide (architecture, conventions,
environment variables). It is written for AI coding assistants but is equally the best
reference for humans.

## Getting set up

Prerequisites: Docker, Go 1.26+, Python 3.12, Node 20+ (only for the web UIs).

```bash
./start.sh                 # platform + playground via Docker Compose
open http://localhost:5173 # 3D dashboard: spawn robots, send commands
./start.sh down
```

For the MLOps path (Argo Workflows, Kubeflow Trainer, Katib) on a laptop, see
"Option C: k3s on Colima" in the [README](README.md). Local Kubernetes commands always pass an
explicit `--context`, so they never touch whatever cluster your shell currently points at; keep
it that way in anything you add.

## Running the tests

These are exactly what CI runs on every pull request:

```bash
# Go (platform and legacy playground simulator)
cd platform   && gofmt -l . && go vet ./... && go test -race ./...
cd playground && go vet ./... && go test -race ./...

# Python suites (MuJoCo tests need MUJOCO_GL=osmesa on headless Linux)
cd platform/training     && pip install -r requirements.txt && python -m pytest -q
cd platform/inference    && pip install -r requirements.txt && python -m pytest -q
cd playground/simulator  && pip install -r requirements.txt && python -m pytest -q
cd platform/sdk/python   && python -m pytest -q

# Shell scripts
shellcheck -S warning start.sh platform/scripts/*.sh && bash platform/scripts/test_k3s_context.sh
```

Go tests that touch Postgres/Redis expect them on localhost (CI starts both as services);
`./start.sh --platform` gives you the same locally.

## How we work

**Test first.** Every change starts with a failing test that describes the behaviour, then the
code that makes it pass. A bug fix comes with the test that would have caught it. Pull requests
without tests for new behaviour will be asked for them.

**Go conventions** (enforced in review):
- Handler → service → repository layering; handlers in `internal/api/` stay thin.
- Dependencies injected through constructors; interfaces defined where they are consumed.
- Wrap errors with context (`fmt.Errorf("get robot %s: %w", id, err)`); don't panic.
- No magic numbers: named constants. Structured logging with `log/slog`.
- Every goroutine exits on `ctx.Done()`. Every external call has a timeout.
- New endpoints or pipeline stages get Prometheus metrics.

**Python:** follow the surrounding module's style; constants at module top with a comment
giving their unit and reason.

**Train/serve parity — the one rule that breaks things silently.** Robots and the MuJoCo test
track must see, score and end episodes identically, or a policy that looks good in evaluation
misbehaves on the fleet. These files exist as byte-identical copies in
`platform/training/` and `playground/simulator/`:

- `observation.py` (the 86-dim policy input)
- `locomotion_reward.py` (reward, fall rule, episode length)
- `policy_meta.py`
- `assets/lab_room.xml` (the scene)

Change both copies in the same commit; `platform/training/test_parity.py` fails CI otherwise.
Changing the observation layout also means bumping `OBS_VERSION` and the experience prefix in
`platform/internal/experience/transition.go` (see CLAUDE.md, "Model Registry").

**Measure, don't assume.** For training or performance changes, include the numbers you
measured (steps, wall time, reward, episode length, and how the robot actually moves) in the
pull request, and say which figures are estimates.

## Pull requests

1. Fork, then branch from `main` (`fix/…`, `feat/…`, `docs/…`).
2. Keep a PR to one concern; refactors go in their own PR.
3. Write the commit message as *why*, not just *what*: the problem, what changed, how you
   verified it.
4. Make sure the test commands above pass locally; CI must be green before review.
5. Update the docs the change affects (README, `platform/docs/`, CLAUDE.md).
6. Fill in the PR description: what and why, how it was tested, and any behaviour change for
   running robots or deployed models.

## Reporting bugs and proposing features

Open an issue with: what you ran, what you expected, what happened (logs, error text), and
your environment (OS, Docker/Colima/kind, Python and Go versions). For larger features, open an
issue to discuss the design before writing code.

Never include secrets, credentials or real kubeconfigs in issues, logs or commits.

## License

By contributing you agree that your contributions are licensed under the project's
[MIT License](LICENSE).
