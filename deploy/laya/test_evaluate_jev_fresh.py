import unittest

from evaluate import SIGNALS
from evaluate_jev_fresh import fresh_inputs, summarize


class FreshProfileTests(unittest.TestCase):
    def test_fresh_labels_lengths_and_no_copied_examples(self):
        rows, questions, model = fresh_inputs()
        self.assertEqual(model, "jev-1.13.0")
        self.assertEqual(set(questions), set(SIGNALS))
        for label, count in {"ordinary": 20, "anti_scam": 12, "role_negative": 8,
                             "context_normal": 8, "name_only_risk": 32, "denial_risk": 4,
                             "adult_control": 4, "role_control": 4, "format_control": 4}.items():
            self.assertEqual(sum(r["slice"] == label for r in rows), count)

    def test_slice_metrics_and_missing_response_rejection(self):
        rows, _, _ = fresh_inputs()
        obs = [{"id": r["id"], "scores": {s: .85 if s in r["labels"] else .1 for s in SIGNALS}} for r in rows]
        got = summarize(rows, obs)
        self.assertEqual(len(got), 180)
        self.assertTrue(all(m["fp"] == m["fn"] == 0 for m in got))
        total = next(m for m in got if m["scope"] == "all" and m["lang"] == "all" and m["signal"] == "any_risk")
        self.assertEqual((total["positives"], total["negatives"]), (48, 48))
        with self.assertRaises(AssertionError):
            summarize(rows, obs[:-1])
        with self.assertRaises(AssertionError):
            summarize(rows, obs[:-1] + obs[:1])


if __name__ == "__main__":
    unittest.main()
