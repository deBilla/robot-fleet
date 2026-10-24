FROM python:3.12-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgl1-mesa-dev libglib2.0-0 libglew-dev \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd -r trainer && useradd -r -g trainer trainer
WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY train_locomotion.py evaluate_policy.py fleetos_env.py ./
COPY train_reach.py evaluate_reach.py fleetos_reach_env.py ./
COPY train_offline.py train_bc.py train_autoencoder.py ./
COPY fleetos_grasp_env.py train_grasp.py evaluate_grasp.py ./
COPY observation.py locomotion_reward.py policy_meta.py policy_action.py benchmark_offline_vs_online.py ./
COPY assets/ ./assets/

USER trainer
