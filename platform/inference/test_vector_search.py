"""Tests for vector search module."""

import unittest
from vector_search import VectorIndex, robot_to_text, text_to_embedding, EMBED_DIM


class TestTextEmbedding(unittest.TestCase):
    def test_embedding_shape(self):
        emb = text_to_embedding("active robot near warehouse")
        self.assertEqual(emb.shape, (EMBED_DIM,))

    def test_embedding_normalized(self):
        emb = text_to_embedding("some text here")
        norm = (emb ** 2).sum() ** 0.5
        self.assertAlmostEqual(norm, 1.0, places=4)

    def test_similar_texts_close(self):
        e1 = text_to_embedding("robot active high battery")
        e2 = text_to_embedding("robot active full battery")
        e3 = text_to_embedding("completely unrelated text about cooking")
        sim_close = float(e1 @ e2)
        sim_far = float(e1 @ e3)
        self.assertGreater(sim_close, sim_far)

    def test_deterministic(self):
        e1 = text_to_embedding("test input")
        e2 = text_to_embedding("test input")
        self.assertTrue((e1 == e2).all())


class TestRobotToText(unittest.TestCase):
    def test_basic_robot(self):
        robot = {
            "robot_id": "robot-0001", "status": "active",
            "battery_level": 0.85, "pos_x": 5.0, "pos_y": 10.0,
            "model": "humanoid-v1",
        }
        text = robot_to_text(robot)
        self.assertIn("robot-0001", text)
        self.assertIn("active", text)
        self.assertIn("high battery", text)
        self.assertIn("x=5.0", text)

    def test_low_battery(self):
        robot = {"robot_id": "r1", "status": "charging", "battery_level": 0.1, "pos_x": 0, "pos_y": 0}
        text = robot_to_text(robot)
        self.assertIn("low battery critical", text)
        self.assertIn("charging", text)

    def test_error_status(self):
        robot = {"robot_id": "r2", "status": "error", "battery_level": 0.5, "pos_x": 0, "pos_y": 0}
        text = robot_to_text(robot)
        self.assertIn("error", text)
        self.assertIn("fault", text)


class TestVectorIndex(unittest.TestCase):
    def setUp(self):
        self.index = VectorIndex()
        self.robots = [
            {"robot_id": "r1", "status": "active", "battery_level": 0.9, "pos_x": 10, "pos_y": 20, "model": "humanoid-v1"},
            {"robot_id": "r2", "status": "charging", "battery_level": 0.1, "pos_x": 0, "pos_y": 0, "model": "humanoid-v1"},
            {"robot_id": "r3", "status": "error", "battery_level": 0.3, "pos_x": -5, "pos_y": 15, "model": "humanoid-v1"},
            {"robot_id": "r4", "status": "idle", "battery_level": 0.6, "pos_x": 30, "pos_y": -10, "model": "humanoid-v1"},
            {"robot_id": "r5", "status": "active", "battery_level": 0.15, "pos_x": 8, "pos_y": 12, "model": "humanoid-v1"},
        ]
        self.index.index_robots(self.robots)

    def test_search_returns_results(self):
        results = self.index.search("active robot", top_k=3)
        self.assertGreater(len(results), 0)
        self.assertLessEqual(len(results), 3)

    def test_search_low_battery(self):
        results = self.index.search("low battery charging", top_k=3)
        robot_ids = [r["robot_id"] for r in results]
        # r2 (charging, 0.1 battery) should appear in top 3
        self.assertIn("r2", robot_ids)

    def test_search_error_robots(self):
        results = self.index.search("error fault broken", top_k=2)
        robot_ids = [r["robot_id"] for r in results]
        self.assertIn("r3", robot_ids)

    def test_search_has_scores(self):
        results = self.index.search("active", top_k=3)
        for r in results:
            self.assertIn("score", r)
            self.assertIn("robot_id", r)
            self.assertIn("description", r)

    def test_empty_index(self):
        empty = VectorIndex()
        results = empty.search("anything", top_k=5)
        self.assertEqual(len(results), 0)

    def test_clear_index(self):
        self.index.clear()
        results = self.index.search("active", top_k=5)
        self.assertEqual(len(results), 0)

    def test_top_k_limit(self):
        results = self.index.search("robot", top_k=2)
        self.assertLessEqual(len(results), 2)


if __name__ == "__main__":
    unittest.main()
