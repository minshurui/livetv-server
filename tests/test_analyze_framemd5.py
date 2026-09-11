import importlib.util
import unittest
from pathlib import Path


def load_analyzer():
    path = Path(__file__).with_name("analyze_framemd5.py")
    spec = importlib.util.spec_from_file_location("analyze_framemd5", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class FrameMD5AnalysisTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.analyzer = load_analyzer()

    def analyze(self, hashes):
        return self.analyzer.analyze(
            hashes,
            fps=2,
            min_frames=6,
            max_freeze_seconds=3,
            max_loop_period_seconds=5,
            min_loop_cycles=3,
            min_loop_span_seconds=5,
        )

    def test_changing_frames_pass(self):
        result = self.analyze([f"frame-{number}" for number in range(20)])
        self.assertTrue(result["passed"], result)
        self.assertEqual(result["unique_frames"], 20)

    def test_frozen_video_fails(self):
        result = self.analyze(["same"] * 10)
        self.assertFalse(result["passed"])
        self.assertIn("连续相同画面", result["problems"][0])

    def test_repeated_short_clip_fails(self):
        pattern = ["a", "b", "c", "d"]
        result = self.analyze(pattern * 4)
        self.assertFalse(result["passed"])
        loop = result["longest_repeated_sequence"]
        self.assertEqual(loop["period_frames"], 4)
        self.assertEqual(loop["cycles"], 4)


if __name__ == "__main__":
    unittest.main()
