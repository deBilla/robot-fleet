"""
FleetOS Vector Search — FAISS-based semantic robot search.

Provides semantic search over robot fleet state. Robots are indexed by
embedding their state descriptions into vector space, enabling natural
language queries like "find robots near the warehouse with low battery."

Uses a simplified TF-IDF-like embedding for demonstration. In production,
replace with a proper sentence transformer (e.g., all-MiniLM-L6-v2).
"""

from __future__ import annotations

import json
import logging
import math
import re
from dataclasses import dataclass
from http.server import HTTPServer, BaseHTTPRequestHandler

import numpy as np

try:
    import faiss
    HAS_FAISS = True
except ImportError:
    HAS_FAISS = False

log = logging.getLogger("vector-search")

EMBED_DIM = 128


@dataclass
class RobotDoc:
    """A robot state document for indexing."""
    robot_id: str
    text: str       # human-readable state description
    embedding: np.ndarray


def robot_to_text(robot: dict) -> str:
    """Convert robot state to a searchable text description."""
    parts = [
        f"robot {robot.get('robot_id', robot.get('ID', ''))}",
        f"status {robot.get('status', robot.get('Status', ''))}",
        f"battery {robot.get('battery_level', robot.get('BatteryLevel', 0)):.0%}",
    ]
    pos_x = robot.get('pos_x', robot.get('PosX', 0))
    pos_y = robot.get('pos_y', robot.get('PosY', 0))
    parts.append(f"position x={pos_x:.1f} y={pos_y:.1f}")

    battery = robot.get('battery_level', robot.get('BatteryLevel', 1.0))
    if battery < 0.2:
        parts.append("low battery critical")
    elif battery < 0.5:
        parts.append("low battery")
    elif battery > 0.8:
        parts.append("high battery full")

    status = robot.get('status', robot.get('Status', ''))
    if status == 'active':
        parts.append("active working moving")
    elif status == 'charging':
        parts.append("charging docked station")
    elif status == 'idle':
        parts.append("idle standby waiting")
    elif status == 'error':
        parts.append("error fault broken offline")

    model = robot.get('model', robot.get('Model', ''))
    if model:
        parts.append(f"model {model}")

    return " ".join(parts)


def text_to_embedding(text: str) -> np.ndarray:
    """
    Convert text to a fixed-size embedding vector.

    Uses a deterministic hash-based approach (simulates sentence embedding).
    In production, replace with: model.encode(text) using sentence-transformers.
    """
    tokens = re.findall(r'\w+', text.lower())
    embedding = np.zeros(EMBED_DIM, dtype=np.float32)

    for i, token in enumerate(tokens):
        # Hash each token to a position in the embedding
        h = hash(token) % (2**31)
        np.random.seed(h)
        token_vec = np.random.randn(EMBED_DIM).astype(np.float32)
        # Weight by position (earlier tokens slightly more important)
        weight = 1.0 / (1.0 + 0.1 * i)
        embedding += weight * token_vec

    # L2 normalize
    norm = np.linalg.norm(embedding)
    if norm > 0:
        embedding /= norm

    return embedding


class VectorIndex:
    """FAISS-backed vector index for semantic robot search."""

    def __init__(self):
        self.docs: list[RobotDoc] = []
        if HAS_FAISS:
            self.index = faiss.IndexFlatIP(EMBED_DIM)  # Inner product (cosine on normalized vecs)
        else:
            self.index = None  # Fallback to brute-force numpy

    def clear(self):
        """Clear the index."""
        self.docs = []
        if HAS_FAISS and self.index is not None:
            self.index.reset()

    def index_robots(self, robots: list[dict]):
        """Index a batch of robots."""
        self.clear()
        embeddings = []
        for robot in robots:
            text = robot_to_text(robot)
            emb = text_to_embedding(text)
            self.docs.append(RobotDoc(
                robot_id=robot.get('robot_id', robot.get('ID', '')),
                text=text,
                embedding=emb,
            ))
            embeddings.append(emb)

        if not embeddings:
            return

        matrix = np.stack(embeddings)
        if HAS_FAISS and self.index is not None:
            self.index.add(matrix)
            log.info("Indexed %d robots with FAISS", len(robots))
        else:
            log.info("Indexed %d robots (numpy fallback, FAISS not available)", len(robots))

    def search(self, query: str, top_k: int = 5) -> list[dict]:
        """Search for robots matching a natural language query."""
        query_emb = text_to_embedding(query).reshape(1, -1)

        if HAS_FAISS and self.index is not None and self.index.ntotal > 0:
            scores, indices = self.index.search(query_emb, min(top_k, len(self.docs)))
            results = []
            for score, idx in zip(scores[0], indices[0]):
                if idx < 0:
                    continue
                doc = self.docs[idx]
                results.append({
                    "robot_id": doc.robot_id,
                    "score": round(float(score), 4),
                    "description": doc.text,
                })
            return results
        else:
            # Numpy fallback
            if not self.docs:
                return []
            matrix = np.stack([d.embedding for d in self.docs])
            scores = (matrix @ query_emb.T).flatten()
            top_indices = np.argsort(-scores)[:top_k]
            return [
                {
                    "robot_id": self.docs[i].robot_id,
                    "score": round(float(scores[i]), 4),
                    "description": self.docs[i].text,
                }
                for i in top_indices
            ]


# --- Singleton index ---
_index = VectorIndex()


class VectorSearchHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        content_len = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(content_len)) if content_len else {}

        if self.path == "/index":
            robots = body.get("robots", [])
            _index.index_robots(robots)
            self._json(200, {"indexed": len(robots), "backend": "faiss" if HAS_FAISS else "numpy"})

        elif self.path == "/search":
            query = body.get("query", "")
            top_k = body.get("top_k", 5)
            results = _index.search(query, top_k)
            self._json(200, {"query": query, "results": results, "total": len(results)})

        else:
            self._json(404, {"error": "not found"})

    def do_GET(self):
        if self.path == "/health":
            self._json(200, {
                "status": "ok",
                "backend": "faiss" if HAS_FAISS else "numpy",
                "indexed": len(_index.docs),
                "embed_dim": EMBED_DIM,
            })
        else:
            self._json(404, {"error": "not found"})

    def _json(self, status, data):
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(data).encode())

    def log_message(self, format, *args):
        pass


def main():
    logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s")
    port = 8082
    log.info("Vector Search Service starting on :%d (backend: %s)", port, "FAISS" if HAS_FAISS else "numpy")
    server = HTTPServer(("0.0.0.0", port), VectorSearchHandler)
    server.serve_forever()


if __name__ == "__main__":
    main()
