FROM python:3.12-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgl1-mesa-dev libglib2.0-0 libglew-dev \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd -r trainer && useradd -r -g trainer trainer
WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY train_locomotion.py evaluate_policy.py fleetos_env.py ./
COPY assets/ ./assets/

USER trainer
