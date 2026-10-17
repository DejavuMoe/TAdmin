import unittest

from evaluate import SIGNALS
from evaluate_jev_runtime import runtime_inputs, checked_scores


class RuntimeBaselineTests(unittest.TestCase):
    def test_exported_questions_and_frozen_baseline(self):
        _, original, sets, baseline = runtime_inputs()
        self.assertEqual(sum(map(len, sets.values())), 140)
        self.assertEqual(len(baseline["observations"]), 140)
        for q in original.values():
            self.assertIsInstance(q["instructions"], dict)
            self.assertEqual(set(q["criteria"]), {"true", "false"})
        self.assertEqual(set(original), set(baseline["questions"]))

    def test_different_model_cannot_enter_comparison(self):
        response = {"model": "jev-1.13.0", "answers": {s: {"type": "noul", "noul": .5} for s in SIGNALS}}
        self.assertEqual(checked_scores(response, "jev-1.13.0"), {s: .5 for s in SIGNALS})
        with self.assertRaises(AssertionError):
            checked_scores(response, "jev-1.14.0")


if __name__ == "__main__":
    unittest.main()
