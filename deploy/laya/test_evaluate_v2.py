import json
import unittest

from evaluate import read_samples, state, SIGNALS
from evaluate_v2 import ROOT, VARIANTS, questions, metrics


class RevisionTests(unittest.TestCase):
    def test_three_question_changes_only(self):
        q = questions()
        self.assertEqual(set(q), set(VARIANTS))
        for key in ("adult", "ad_formatting"):
            self.assertEqual(q[VARIANTS[0]][key], q[VARIANTS[1]][key])

    def test_new_set_is_disjoint_and_balanced(self):
        old = read_samples(ROOT / "eval/profiles.jsonl")
        new = read_samples(ROOT / "eval/profiles-new-v2.jsonl")
        self.assertEqual(len(new), 60)
        old_states = {json.dumps(state(r), sort_keys=True) for r in old}
        new_states = {json.dumps(state(r), sort_keys=True) for r in new}
        self.assertEqual(len(new_states), 60)
        self.assertFalse(old_states & new_states)
        self.assertFalse({r["id"] for r in old} & {r["id"] for r in new})
        for lang in ("zh", "en"):
            rows = [r for r in new if r["lang"] == lang]
            self.assertEqual(len(rows), 30)
            self.assertEqual(sum(not r["labels"] for r in rows), 8)
            self.assertEqual({s: sum(s in r["labels"] for r in rows) for s in SIGNALS},
                             {"promotion": 14, "scam": 4, "adult": 2, "impersonation": 8, "ad_formatting": 2})
        for r in new:
            self.assertNotIn("slice", state(r))

    def test_pair_metrics_and_duplicate_rejection(self):
        rows = [{"id": "a", "lang": "zh", "labels": ["scam"]},
                {"id": "b", "lang": "en", "labels": []}]
        obs = [{"id": r["id"], "variant": v, "scores": {k: .9 for k in SIGNALS}}
               for r in rows for v in VARIANTS]
        got = metrics(rows, obs)
        self.assertEqual(len(got), 60)
        for m in got:
            self.assertEqual(m["tp"] + m["tn"] + m["fp"] + m["fn"], 2 if m["lang"] == "all" else 1)
            if m["lang"] == "all" and m["signal"] == "scam":
                self.assertEqual((m["tp"], m["fp"], m["fn"]), (1, 1, 0))
        with self.assertRaises(AssertionError):
            metrics(rows, obs + obs[:1])
        with self.assertRaises(AssertionError):
            metrics(rows, obs[:-1])


if __name__ == "__main__":
    unittest.main()
