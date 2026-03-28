"""
FleetOS Inference Service — Educational Robot AI Pipeline

This service implements a simplified but realistic version of how
robot AI inference works (inspired by NVIDIA GR00T-N1).

Pipeline stages:
  1. Vision Encoder  — processes camera image into visual embeddings
  2. Language Encoder — processes instruction into text embeddings
  3. Cross-Attention  — fuses vision + language into a joint representation
  4. Diffusion Policy — iteratively denoises random noise into action trajectories

Each stage logs its tensor shapes and timing so you can trace
exactly what happens during inference.
"""

from __future__ import annotations

import base64
import io
import json
import logging
import math
import time
from dataclasses import dataclass
from http.server import HTTPServer, BaseHTTPRequestHandler

import numpy as np

# ─── Configuration ───────────────────────────────────────────────

HUMANOID_JOINTS = [
    "left_shoulder_pitch", "left_shoulder_roll", "left_shoulder_yaw",
    "left_elbow", "left_wrist_roll",
    "right_shoulder_pitch", "right_shoulder_roll", "right_shoulder_yaw",
    "right_elbow", "right_wrist_roll",
    "left_hip_pitch", "left_hip_roll", "left_hip_yaw",
    "left_knee", "left_ankle",
    "right_hip_pitch", "right_hip_roll", "right_hip_yaw",
    "right_knee", "right_ankle",
]

ACTION_HORIZON = 16    # predict 16 future timesteps
ACTION_DIM = len(HUMANOID_JOINTS)  # 20 joints
DIFFUSION_STEPS = 10   # number of denoising steps
IMAGE_SIZE = 224        # expected input image size
EMBED_DIM = 512         # embedding dimension
NUM_PATCHES = 196       # 14x14 vision transformer patches

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    datefmt="%H:%M:%S",
)
log = logging.getLogger("inference")


# ─── Stage 1: Vision Encoder ────────────────────────────────────
#
# In a real model (e.g., Eagle-2 VLM used by GR00T-N1):
#   - Input: RGB image (3, 224, 224)
#   - Split into 14×14 = 196 patches of 16×16 pixels
#   - Each patch projected to 512-dim embedding via learned linear layer
#   - Positional embeddings added
#   - Passed through 12 transformer blocks
#   - Output: (196, 512) — 196 patch embeddings
#
# Our simplified version:
#   - Simulates the projection + positional encoding
#   - Uses random projection (stands in for learned weights)

@dataclass
class VisionOutput:
    patch_embeddings: np.ndarray   # shape: (NUM_PATCHES, EMBED_DIM)
    cls_token: np.ndarray          # shape: (EMBED_DIM,)


def vision_encode(image_bytes: bytes | None) -> VisionOutput:
    """
    Stage 1: Encode camera image into patch embeddings.

    Real model: ViT-L/14 or Eagle-2 vision encoder
    - Splits 224×224 image into 14×14 grid of 16×16 patches
    - Projects each patch into 512-dim space
    - Adds learned positional embeddings so model knows spatial layout
    - Self-attention across patches captures relationships
    """
    t0 = time.perf_counter()

    if image_bytes:
        # Simulate: decode image → resize to 224×224 → normalize
        # In reality: pixel_values = transforms(PIL.Image) → (3, 224, 224)
        np.random.seed(hash(image_bytes[:32]) % 2**32)
        pixel_proxy = np.random.randn(3, IMAGE_SIZE, IMAGE_SIZE).astype(np.float32)
    else:
        # No image provided — use zero observation (robot has no camera input)
        pixel_proxy = np.zeros((3, IMAGE_SIZE, IMAGE_SIZE), dtype=np.float32)

    log.info("  [Vision] Input tensor shape: (3, %d, %d) — RGB image", IMAGE_SIZE, IMAGE_SIZE)

    # Step 1a: Patch embedding — split into 14×14 patches, project each to EMBED_DIM
    # Real: nn.Conv2d(3, 512, kernel_size=16, stride=16) — one conv = patch + project
    patch_size = IMAGE_SIZE // 14  # = 16
    patches = pixel_proxy[:, :14*patch_size, :14*patch_size]  # crop to exact grid
    patches = patches.reshape(3, 14, patch_size, 14, patch_size)
    patches = patches.transpose(1, 3, 0, 2, 4).reshape(NUM_PATCHES, -1)  # (196, 768)

    # Project patches to embedding dim (simulates learned projection matrix)
    proj_matrix = np.random.randn(patches.shape[1], EMBED_DIM).astype(np.float32) * 0.02
    patch_embeddings = patches @ proj_matrix  # (196, 512)

    log.info("  [Vision] Patches: %d (14×14 grid), each projected to %d-dim", NUM_PATCHES, EMBED_DIM)

    # Step 1b: Add positional embeddings — so model knows WHERE each patch is
    # Real: learned (196, 512) parameter added element-wise
    positions = np.array([
        [math.sin(pos / 10000 ** (2*i/EMBED_DIM)) if i % 2 == 0
         else math.cos(pos / 10000 ** (2*i/EMBED_DIM))
         for i in range(EMBED_DIM)]
        for pos in range(NUM_PATCHES)
    ], dtype=np.float32)
    patch_embeddings += positions

    log.info("  [Vision] Added sinusoidal positional encoding: (%d, %d)", NUM_PATCHES, EMBED_DIM)

    # Step 1c: CLS token — global image representation (mean pool of patches)
    cls_token = patch_embeddings.mean(axis=0)  # (512,)

    elapsed = (time.perf_counter() - t0) * 1000
    log.info("  [Vision] Output: patch_embeddings(%d, %d), cls_token(%d,) — %.1fms",
             NUM_PATCHES, EMBED_DIM, EMBED_DIM, elapsed)

    return VisionOutput(patch_embeddings=patch_embeddings, cls_token=cls_token)


# ─── Stage 2: Language Encoder ──────────────────────────────────
#
# In a real model:
#   - Tokenize instruction → token IDs
#   - Embed tokens → (seq_len, 512)
#   - Pass through transformer → contextual embeddings
#   - Pool into single instruction vector
#
# Our simplified version:
#   - Character-level "tokenization" (educational stand-in)
#   - Projects to embedding space
#   - Mean-pools to instruction vector

@dataclass
class LanguageOutput:
    token_embeddings: np.ndarray   # shape: (seq_len, EMBED_DIM)
    instruction_vec: np.ndarray    # shape: (EMBED_DIM,)


def language_encode(instruction: str) -> LanguageOutput:
    """
    Stage 2: Encode natural language instruction into embeddings.

    Real model: LLM backbone (e.g., Qwen-2 7B in GR00T-N1)
    - Tokenizes text into subword tokens
    - Embeds each token into 512-dim space
    - Self-attention captures word relationships
    - "pick up the red cup" → model understands object + action + attribute
    """
    t0 = time.perf_counter()

    # Tokenization: split into words (real model uses BPE/SentencePiece)
    tokens = instruction.lower().split()
    seq_len = len(tokens)
    log.info("  [Language] Instruction: '%s' → %d tokens", instruction, seq_len)

    # Token embedding: each token gets a EMBED_DIM vector
    # Real: nn.Embedding(vocab_size=32000, embedding_dim=512)
    # We use a hash-based deterministic embedding (same word → same vector)
    token_embeddings = np.zeros((seq_len, EMBED_DIM), dtype=np.float32)
    for i, tok in enumerate(tokens):
        np.random.seed(hash(tok) % 2**32)
        token_embeddings[i] = np.random.randn(EMBED_DIM).astype(np.float32) * 0.1

    log.info("  [Language] Token embeddings shape: (%d, %d)", seq_len, EMBED_DIM)

    # Positional encoding (same sinusoidal as vision)
    for pos in range(seq_len):
        for i in range(EMBED_DIM):
            if i % 2 == 0:
                token_embeddings[pos, i] += math.sin(pos / 10000 ** (2*i/EMBED_DIM))
            else:
                token_embeddings[pos, i] += math.cos(pos / 10000 ** (2*i/EMBED_DIM))

    # Pool to single instruction vector (mean of all token embeddings)
    # Real model might use [CLS] token or last-token pooling
    instruction_vec = token_embeddings.mean(axis=0)  # (512,)

    elapsed = (time.perf_counter() - t0) * 1000
    log.info("  [Language] Output: token_embeddings(%d, %d), instruction_vec(%d,) — %.1fms",
             seq_len, EMBED_DIM, EMBED_DIM, elapsed)

    return LanguageOutput(token_embeddings=token_embeddings, instruction_vec=instruction_vec)


# ─── Stage 3: Cross-Attention Fusion ────────────────────────────
#
# In a real model:
#   - Vision embeddings attend to language embeddings (and vice versa)
#   - The model learns WHICH patches are relevant to the instruction
#   - "pick up the RED CUP" → attention peaks on cup-shaped red patches
#   - Output: fused representation capturing both visual scene and task
#
# Our simplified version:
#   - Scaled dot-product attention between vision and language
#   - Produces a conditioning vector for the diffusion policy


def cross_attention_fuse(vision: VisionOutput, language: LanguageOutput) -> np.ndarray:
    """
    Stage 3: Fuse vision and language via cross-attention.

    This is where the model connects "what it sees" with "what to do."

    Attention mechanism:
      Q = vision patches        (196, 512)  — "what am I looking at?"
      K = language tokens        (seq, 512)  — "what are the key concepts?"
      V = language tokens        (seq, 512)  — "what information to extract?"

      Attention = softmax(Q @ K^T / √d) @ V
      → Each vision patch attends to relevant words
      → Output shape: (196, 512) — vision patches enriched with language context

    Finally pool into a single conditioning vector for the diffusion model.
    """
    t0 = time.perf_counter()

    Q = vision.patch_embeddings    # (196, 512) — queries from vision
    K = language.token_embeddings  # (seq, 512) — keys from language
    V = language.token_embeddings  # (seq, 512) — values from language

    log.info("  [CrossAttn] Q(vision): (%d, %d), K(language): (%d, %d)",
             Q.shape[0], Q.shape[1], K.shape[0], K.shape[1])

    # Scaled dot-product attention: softmax(QK^T / √d) @ V
    scale = math.sqrt(EMBED_DIM)
    attn_scores = (Q @ K.T) / scale        # (196, seq_len)
    # Softmax along key dimension
    attn_scores -= attn_scores.max(axis=-1, keepdims=True)  # numerical stability
    attn_weights = np.exp(attn_scores) / np.exp(attn_scores).sum(axis=-1, keepdims=True)

    log.info("  [CrossAttn] Attention map shape: (%d, %d) — each patch attends to each word",
             attn_weights.shape[0], attn_weights.shape[1])

    # Apply attention: weighted sum of language values per vision patch
    attended = attn_weights @ V  # (196, 512)

    # Residual connection + layer norm (standard transformer practice)
    fused = Q + attended  # (196, 512)
    fused = fused / (np.linalg.norm(fused, axis=-1, keepdims=True) + 1e-6)  # layer norm approx

    # Pool into conditioning vector for diffusion
    condition_vec = fused.mean(axis=0)  # (512,)

    # Also concatenate the language instruction vector for richer conditioning
    condition_vec = np.concatenate([condition_vec, language.instruction_vec])  # (1024,)

    elapsed = (time.perf_counter() - t0) * 1000
    log.info("  [CrossAttn] Output: condition_vec(%d,) — %.1fms", condition_vec.shape[0], elapsed)

    return condition_vec


# ─── Stage 4: Diffusion Policy (Action Head) ────────────────────
#
# This is the core innovation. Instead of directly predicting actions,
# we use DIFFUSION — the same technique as Stable Diffusion for images,
# but applied to robot action trajectories.
#
# How it works:
#   1. Start with pure random noise shaped like an action trajectory
#      noise ~ N(0, 1), shape: (ACTION_HORIZON, ACTION_DIM) = (16, 20)
#
#   2. Gradually denoise over DIFFUSION_STEPS iterations:
#      For step t from T down to 1:
#        predicted_clean = model(noisy_actions, t, condition)
#        noisy_actions = denoise_step(noisy_actions, predicted_clean, t)
#
#   3. After all steps: clean action trajectory emerges from noise
#
# Why diffusion for robotics?
#   - Robot tasks are MULTIMODAL — many valid ways to reach for a cup
#   - Diffusion naturally samples from this distribution
#   - Produces smooth, physically plausible trajectories
#   - Better than single-point prediction (regression) which averages modes

@dataclass
class DiffusionOutput:
    actions: np.ndarray         # shape: (ACTION_HORIZON, ACTION_DIM)
    denoising_trajectory: list  # list of noise levels at each step (for visualization)


def diffusion_policy(condition_vec: np.ndarray, instruction: str) -> DiffusionOutput:
    """
    Stage 4: Denoise random noise into a smooth action trajectory.

    This simulates the DDPM (Denoising Diffusion Probabilistic Model)
    process used in Diffusion Policy and GR00T-N1.

    The conditioning vector tells the model WHAT to do.
    The diffusion process figures out HOW to move the joints.
    """
    t0 = time.perf_counter()

    # Step 4a: Start with pure Gaussian noise
    # Shape: (16 timesteps, 20 joints) — the action trajectory to be denoised
    np.random.seed(int(time.time() * 1000) % 2**32)
    x_t = np.random.randn(ACTION_HORIZON, ACTION_DIM).astype(np.float32)

    log.info("  [Diffusion] Initial noise shape: (%d, %d) — %d timesteps × %d joints",
             ACTION_HORIZON, ACTION_DIM, ACTION_HORIZON, ACTION_DIM)
    log.info("  [Diffusion] Noise stats: mean=%.3f, std=%.3f (pure random)",
             x_t.mean(), x_t.std())

    # Noise schedule: linear beta schedule (standard DDPM)
    # beta_t controls how much noise to remove at each step
    betas = np.linspace(0.0001, 0.02, DIFFUSION_STEPS)
    alphas = 1.0 - betas
    alpha_cumprod = np.cumprod(alphas)

    log.info("  [Diffusion] Schedule: %d steps, beta range [%.4f, %.4f]",
             DIFFUSION_STEPS, betas[0], betas[-1])

    denoising_trajectory = [float(np.abs(x_t).mean())]

    # Step 4b: Iterative denoising — this is where the magic happens
    for step in range(DIFFUSION_STEPS - 1, -1, -1):
        # In a real model: predicted_noise = neural_net(x_t, timestep=step, condition=condition_vec)
        # The neural net (typically a transformer) predicts what noise was added
        # We simulate this with a condition-dependent noise prediction

        # Simulate the neural network's noise prediction
        # Real: 8-layer transformer with cross-attention to condition_vec
        cond_influence = condition_vec[:EMBED_DIM].reshape(1, -1)  # (1, 512)

        # Project condition to action space: (1, 512) @ (512, 20) → (1, 20)
        np.random.seed(step * 42 + hash(instruction) % 2**16)
        proj = np.random.randn(EMBED_DIM, ACTION_DIM).astype(np.float32) * 0.01
        target_action = (cond_influence @ proj).squeeze()  # (20,) — what the model thinks the action should be

        # Create instruction-specific action biases
        target_action = _instruction_bias(instruction, target_action)

        # DDPM denoising step
        alpha_t = alpha_cumprod[step]
        predicted_noise = (x_t - math.sqrt(alpha_t) * np.tile(target_action, (ACTION_HORIZON, 1))) / max(math.sqrt(1 - alpha_t), 1e-6)

        # Remove predicted noise (the core denoising operation)
        beta_t = betas[step]
        x_t = (1 / math.sqrt(alphas[step])) * (x_t - beta_t / math.sqrt(1 - alpha_t) * predicted_noise)

        # Add small noise for all steps except the last (stochastic sampling)
        if step > 0:
            noise_scale = math.sqrt(beta_t)
            x_t += noise_scale * np.random.randn(*x_t.shape).astype(np.float32)

        noise_level = float(np.abs(x_t).mean())
        denoising_trajectory.append(noise_level)

        if step % 3 == 0 or step == 0:
            log.info("  [Diffusion] Step %2d/%d — noise_level=%.3f, mean_action=%.3f",
                     DIFFUSION_STEPS - step, DIFFUSION_STEPS, noise_level, x_t.mean())

    # Step 4c: Clamp to physically valid joint ranges
    # Real robots have joint limits (e.g., elbow: -2.0 to 0.5 rad)
    x_t = np.clip(x_t, -2.0, 2.0)

    # Smooth the trajectory (moving average) for physically plausible motion
    kernel = np.array([0.15, 0.7, 0.15])
    for j in range(ACTION_DIM):
        x_t[:, j] = np.convolve(x_t[:, j], kernel, mode='same')

    elapsed = (time.perf_counter() - t0) * 1000
    log.info("  [Diffusion] Final trajectory: (%d, %d) — %.1fms",
             x_t.shape[0], x_t.shape[1], elapsed)
    log.info("  [Diffusion] Action stats: mean=%.3f, std=%.3f, range=[%.3f, %.3f]",
             x_t.mean(), x_t.std(), x_t.min(), x_t.max())

    return DiffusionOutput(actions=x_t, denoising_trajectory=denoising_trajectory)


def _instruction_bias(instruction: str, base_action: np.ndarray) -> np.ndarray:
    """
    Apply instruction-specific biases to make outputs more realistic.
    In a real model, the neural net learns these patterns from data.
    Here we encode some basic motion priors.
    """
    inst = instruction.lower()
    action = base_action.copy()

    # Joint indices for readability
    L_SHOULDER_P, L_SHOULDER_R = 0, 1
    L_ELBOW = 3
    R_SHOULDER_P, R_SHOULDER_R = 5, 6
    R_ELBOW = 8
    L_HIP_P, L_KNEE = 10, 13
    R_HIP_P, R_KNEE = 15, 18

    if "wave" in inst:
        action[R_SHOULDER_P] = 1.2   # raise right arm
        action[R_SHOULDER_R] = -0.5
        action[R_ELBOW] = -0.8       # bend elbow
    elif "pick" in inst or "grab" in inst or "grasp" in inst:
        action[L_SHOULDER_P] = 0.6   # both arms forward
        action[R_SHOULDER_P] = 0.6
        action[L_ELBOW] = -0.4       # slight bend
        action[R_ELBOW] = -0.4
    elif "walk" in inst or "move" in inst or "go" in inst:
        action[L_HIP_P] = -0.3       # alternating leg motion
        action[R_HIP_P] = 0.3
        action[L_KNEE] = 0.4
        action[R_KNEE] = 0.1
    elif "sit" in inst:
        action[L_HIP_P] = -1.2       # bend at hips
        action[R_HIP_P] = -1.2
        action[L_KNEE] = 1.5         # bend knees
        action[R_KNEE] = 1.5
    elif "dance" in inst:
        action[L_SHOULDER_P] = 0.8
        action[R_SHOULDER_P] = -0.3
        action[L_HIP_P] = -0.2
        action[R_HIP_P] = 0.2
    elif "bow" in inst:
        action[L_HIP_P] = -0.8
        action[R_HIP_P] = -0.8
    elif "jump" in inst:
        action[L_KNEE] = -0.5        # crouch then extend
        action[R_KNEE] = -0.5
        action[L_HIP_P] = 0.3
        action[R_HIP_P] = 0.3

    return action


# ─── Full Pipeline ───────────────────────────────────────────────

def run_inference(image_b64: str, instruction: str, model_id: str, embodiment: str) -> dict:
    """
    Run the full inference pipeline:
      Image + Instruction → Vision → Language → Fusion → Diffusion → Actions
    """
    pipeline_start = time.perf_counter()

    log.info("=" * 60)
    log.info("INFERENCE REQUEST")
    log.info("  Model: %s | Embodiment: %s", model_id, embodiment)
    log.info("  Instruction: '%s'", instruction)
    log.info("  Image: %s", f"{len(image_b64)} chars base64" if image_b64 else "none")
    log.info("-" * 60)

    # Decode image if provided
    image_bytes = base64.b64decode(image_b64) if image_b64 else None

    # Stage 1: Vision
    log.info("Stage 1/4: VISION ENCODER")
    vision_out = vision_encode(image_bytes)

    # Stage 2: Language
    log.info("Stage 2/4: LANGUAGE ENCODER")
    language_out = language_encode(instruction)

    # Stage 3: Cross-Attention
    log.info("Stage 3/4: CROSS-ATTENTION FUSION")
    condition_vec = cross_attention_fuse(vision_out, language_out)

    # Stage 4: Diffusion
    log.info("Stage 4/4: DIFFUSION POLICY (denoising %d steps)", DIFFUSION_STEPS)
    diffusion_out = diffusion_policy(condition_vec, instruction)

    total_ms = (time.perf_counter() - pipeline_start) * 1000

    # Format output — first timestep actions as the immediate command
    predicted_actions = []
    for j, joint_name in enumerate(HUMANOID_JOINTS):
        predicted_actions.append({
            "joint": joint_name,
            "position": round(float(diffusion_out.actions[0, j]), 4),
            "velocity": round(float(diffusion_out.actions[1, j] - diffusion_out.actions[0, j]) * 10, 4),
            "torque": round(abs(float(diffusion_out.actions[0, j])) * 5, 4),
        })

    # Compute confidence from denoising convergence
    noise_reduction = diffusion_out.denoising_trajectory[0] / max(diffusion_out.denoising_trajectory[-1], 0.01)
    confidence = round(min(0.99, 0.5 + 0.05 * noise_reduction), 2)

    log.info("-" * 60)
    log.info("RESULT: %d joint actions, confidence=%.2f, latency=%.0fms",
             len(predicted_actions), confidence, total_ms)
    log.info("=" * 60)

    return {
        "predicted_actions": predicted_actions,
        "confidence": confidence,
        "model_id": model_id,
        "model_version": "v1.5.0",
        "embodiment": embodiment,
        "action_horizon": ACTION_HORIZON,
        "action_dim": ACTION_DIM,
        "diffusion_steps": DIFFUSION_STEPS,
        "latency_ms": round(total_ms, 1),
        "pipeline_stages": {
            "vision_patches": NUM_PATCHES,
            "embed_dim": EMBED_DIM,
            "condition_dim": EMBED_DIM * 2,
            "denoising_trajectory": [round(x, 3) for x in diffusion_out.denoising_trajectory],
        },
    }


# ─── HTTP Server ─────────────────────────────────────────────────

class InferenceHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path == "/predict":
            content_len = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(content_len)) if content_len else {}

            result = run_inference(
                image_b64=body.get("image", ""),
                instruction=body.get("instruction", "stand still"),
                model_id=body.get("model_id", "groot-n1-v1.5"),
                embodiment=body.get("embodiment", "humanoid-v1"),
            )

            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps(result).encode())
        else:
            self.send_error(404)

    def do_GET(self):
        if self.path == "/health":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"status": "ok", "model": "groot-n1-v1.5"}).encode())
        else:
            self.send_error(404)

    def log_message(self, format, *args):
        # Suppress default HTTP logging — our pipeline logs are more useful
        pass


def main():
    port = 8081
    log.info("FleetOS Inference Service starting on :%d", port)
    log.info("Pipeline: Vision(%d patches) → Language → CrossAttn → Diffusion(%d steps)",
             NUM_PATCHES, DIFFUSION_STEPS)
    log.info("Output: %d timesteps × %d joints = %d action values per inference",
             ACTION_HORIZON, ACTION_DIM, ACTION_HORIZON * ACTION_DIM)
    server = HTTPServer(("0.0.0.0", port), InferenceHandler)
    server.serve_forever()


if __name__ == "__main__":
    main()
