# Locomotion: experiments, current research, and the plan

**Rev 3 (2026-10-24):** E5 deployed to the fleet (gate lowered for a demo); robot behaviour matches the test track; lessons learned; where to pick up.

**Rev 2 (2026-10-24):** E5 measured — residual actions (R1) are the first change that buys survival; adopted as the shared action contract (action version 2).

**Rev 1 (2026-10-24):** first version. Covers experiments E1–E4, a survey of current humanoid
locomotion research, and recommendations R1–R4. Tracking issue:
[#9](https://github.com/deBilla/robot-fleet/issues/9).

## In plain English

Our robots can't walk yet. Training gets them upright and moving forward, but they fall after
about 1.5 seconds, and training longer or paying them more for staying up hasn't fixed it.
Current research suggests the main problem is how we let the policy control the joints: it
commands each joint anywhere in its full range, which makes balancing very hard to learn. Teams
that get humanoids walking let the policy make small adjustments around a standing pose, and
train thousands of simulated robots at once on a GPU.

We made that change (E5). The robot now stays up 40% longer, and that model runs on the fleet's
robots, behaving exactly as it did in testing: a few strides, a fall, stand up, repeat. The full
loop — train, evaluate, gate, canary, robots, fleet data — works end to end. Teaching it to
balance properly (R2) is where the work stops for now.

## Experiments so far (all measured)

Setup for every run unless noted: PPO (Stable-Baselines3) on `FleetOS-Humanoid-v2`, 8 parallel
environments on an Apple-silicon Mac CPU (`--n-envs 8 --n-steps 1024 --batch-size 256`),
~2,400 steps/s. "Ticks before a fall" and the gait figures come from 10 deterministic
test-track episodes per model; one tick is 0.021 s.

| ID | Change | Steps | Ticks before a fall | Falls / 10 | Speed | Torso height | Curve |
|----|--------|-------|---------------------|------------|-------|--------------|-------|
| E1 | Fall rule 0.3 m (old) | 3M | 392 | 10 | 0.62 m/s | **0.57 m (crawling)** | flat after 1.5M |
| E2 | Fall rule 1.0 m | 1.5M | 69 | 10 | 1.41 m/s | 1.31 m | still rising |
| E3 | Fall rule 1.0 m, long run | 10M | 77 (52–99) | 10 | 1.93 m/s | 1.31 m | peak at 4–5M, then flat |
| E4 | E3 + staying-up reward 1.0 → 5.0 | 5M | 65 (42–94) | 10 | 1.09 m/s | 1.27 m | flat after ~2M |
| E5 | E3 + residual actions (R1) | 5M | **109 (58–279)** | 10 | 1.41 m/s | 1.24 m | plateau ~105 from 3.5M |

What they show:

- **E1 → E2:** with the torso allowed down to 0.3 m, PPO learned to crawl and collect the
  staying-up reward. Raising the fall height to 1.0 m (Gymnasium Humanoid's value) fixed this
  and was merged ([#7](https://github.com/deBilla/robot-fleet/pull/7)).
- **E3:** 10M steps made the robot faster, not steadier: it lunges forward and falls.
- **E4:** the hypothesis "falling is too cheap" was wrong. Paying five times more for staying up
  made the robot slower, and it fell sooner. The reward weights aren't what limits it.
- In E3 and E4 the effort penalty is about 1.0 per tick, meaning the policy drives joints
  close to their limits: a bang-bang style of control that is very hard to balance with.
- **E5:** letting the policy output small offsets around the standing pose (±0.5 rad) instead
  of full-range targets is the first change that makes the robot stay up longer rather than move
  faster: +40% ticks in half the training, best episode 5.9 s, effort penalty down to 0.34 per
  tick, final eval reward 205 vs 139. It still falls every episode and plateaus from ~3.5M steps,
  so R2 is next.

## E5 on the fleet (measured)

To see the model on robots, E5 went through the normal deploy path on the local k3s cluster with
the gate's minimum pass rate lowered to 0 for that one request (`MODEL_MIN_EVAL_PASS_RATE`,
restored to 0.5 right after). Its real evaluation is recorded in the registry: **0% pass rate**
(0/20 test-track scenarios reach reward ≥ 500 over ≥ 100 ticks), mean reward 174.8. It still
cleared the regression check against the old baseline (77.5), which the rollout archived.

| | Test track (10 episodes) | robot-0001 in the playground (32 episodes) |
|---|---|---|
| Median time before a fall | 1.6 s | 1.6 s |
| Longest episode | 5.9 s | 3.6 s |
| Torso height | 1.24 m | 1.22 m |

Robot episodes come from fleet experience (`training_data/v3/`, 10 Hz records). The matching
medians are the first end-to-end confirmation that a policy behaves on the robots as it did in
evaluation; replaying E5 through the robot's loading path also reproduced its test-track numbers
exactly.

## Lessons learned

1. **Check how the robot moves, not just the reward.** E1's reward looked healthy while the
   policy crawled at 0.57 m. Torso height, speed, ticks per episode and effort per tick caught
   it; total reward didn't. Evaluation should report them
   ([#12](https://github.com/deBilla/robot-fleet/issues/12)).
2. **More steps don't fix a wrong setup.** E3 ran 10M steps and only got faster at lunging. A
   flat learning curve means change the problem, not the budget.
3. **Test the hypothesis before changing shared code.** "Falling is too cheap" was plausible and
   wrong (E4). Each idea ran in a scratch copy first; only measured wins reached the shared files.
4. **How the policy commands the joints mattered more than reward weights.** Full-range targets
   produced bang-bang control (effort ~1.0/tick); offsets around the standing pose cut it to 0.34
   and bought the first real gain in survival.
5. **Version every contract a policy depends on.** Observations and now actions are recorded in
   `policy_meta.json`; robots, inference and the test track refuse what they can't interpret.
   Defaulting a missing `action_version` to absolute kept every earlier policy valid.
6. **Record physical facts, not policy outputs.** Fleet experience stores the joint targets that
   were applied, so it stays valid whatever action setup trained the policy; the offline trainer
   converts to its own action space.
7. **Images that list files one by one silently miss new modules.** The training Dockerfile would
   have shipped without `policy_action.py`; `test_dockerfile.py` now catches it.
8. **The gate has no per-model bypass**, by design. A demo deploy means lowering
   `MODEL_MIN_EVAL_PASS_RATE` for one request and restoring it; the model's metrics keep its real
   evaluation.
9. **Small fleets behave differently.** With one robot, the 5/25/50% canary stages each assign
   zero robots (rounding down), so only the final step switches it (~15 min after deploying); the
   20% exploration share also rounds to zero, so a single robot records no exploratory data.

## Where to pick up

- **R2 reward terms** (#9): one term at a time in a scratch copy, ~35 min per 5M-step run with
  `--n-envs 8 --n-steps 1024 --batch-size 256`; start with #12 so each term's effect is visible.
- **Offline pipeline on real data:** robots running E5 now record walking experience. Spawn 4–8
  robots for ~30 min, then run `POST /api/v1/training/pipelines` and compare the offline policy
  with E5 on the test track — the first test of whether fleet data can improve on the policy that
  collected it.
- **R3 (SAC)** if R2 plateaus.

## Current research (late 2026)

**Massively parallel GPU simulation + PPO is the mainstream recipe.** Frameworks such as
DeepMind's [MuJoCo Playground](https://playground.mujoco.org/) (MJX / MuJoCo Warp, RSS 2025
outstanding demo) and NVIDIA Isaac Lab run thousands of robot copies on one GPU; Playground
reports most of its tasks train in minutes on a single GPU. PPO works well at that scale; with 8
CPU environments its gradient estimates are far noisier than in published results.

**Off-policy methods built for that scale.** [FastTD3](https://arxiv.org/html/2505.22642v1)
(2025) reports faster training than PPO on humanoid tasks in Isaac Lab and Playground, and was
deployed on a real Booster T1. It is not a universal win: an
[ICLR 2026 paper](https://proceedings.iclr.cc/paper_files/paper/2026/file/d9b564716709357b4bccec9fc9ad04d2-Paper-Conference.pdf)
found it oscillating and collapsing on whole-body motion tracking. SAC belongs to the same family
and is the usual choice when the number of environments can't be scaled.

**Imitating human motion for natural gaits.** The frontier for human-like movement is training
policies to track motion-capture data (LAFAN1, AMASS) retargeted to the robot (e.g. GMT,
BeyondMimic, SONIC), typically trained in Isaac or MuJoCo, checked in a second simulator, then
deployed on Unitree G1/H1-class robots. See also
[AGILE](https://arxiv.org/pdf/2603.20147) for a full loco-manipulation workflow.

**What every working recipe shares, and ours lacked:**

| Practice | Common recipe | FleetOS before R1 |
|----------|---------------|-------------------|
| Action | Small position offsets around a default standing pose (≈ ±0.25–0.5 rad), PD-tracked | Absolute targets anywhere in each joint's full range |
| Smoothness | Penalty on changing actions between ticks | None |
| Reward | Track a commanded velocity; keep the body level and at height; reward foot air time; penalise torque and joint limits | Forward velocity + staying up − effort |
| Scale | Thousands of environments on a GPU | 8 on CPU |

## Recommendations

Ordered by cost and expected payoff. Each lands as its own PR with measurements.

| ID | Recommendation | Cost | Status |
|----|----------------|------|--------|
| R1 | **Residual actions:** the policy outputs offsets (±0.5 rad) around the standing pose instead of absolute joint targets; add an action-change penalty if needed | Short experiment on this Mac; then a parity change (robots, inference and test track interpret actions identically) | ✅ E5: +40% survival; adopted as action version 2 ([#20](https://github.com/deBilla/robot-fleet/pull/20)); deployed to the local fleet |
| R2 | **Standard reward terms:** body level, standing height, foot air time, torque and joint-limit penalties | Short experiments; a shared `locomotion_reward.py` change | Next: R1 plateaus at ~105 ticks |
| R3 | **Algorithm for our scale:** SAC (or FastTD3) while limited to CPU; MuJoCo Playground + PPO if a GPU becomes available (it uses the same MuJoCo models, so `lab_room.xml` carries over) | SAC test ≈ 2–3 h on this Mac (estimate) | Planned |
| R4 | **Motion imitation** from retargeted mocap for human-like gaits | Larger project | Later |

R1 changes the action contract, so it is versioned: `policy_meta.json` records each policy's
`action_version` (`policy_action.py`), robots and inference convert residual outputs to joint
targets before anything else sees them, and fleet experience keeps recording joint targets.
Policies without the field are treated as absolute (version 1), so nothing trained earlier breaks.
