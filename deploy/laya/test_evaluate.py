import copy
import json
from pathlib import Path
import tempfile
import unittest

from evaluate import MODEL, SIGNALS, confusion, read_samples, scores, state, summarize


class EvaluationTests(unittest.TestCase):
    def test_labels_and_lengths(self):
        rows = read_samples(Path(__file__).parent / "eval/profiles.jsonl")
        self.assertEqual(len(rows), 80)
        for lang in ("zh", "en"):
            subset = [r for r in rows if r["lang"] == lang]
            self.assertEqual(len(subset), 40)
            self.assertEqual(sum(not r["labels"] for r in subset), 15)
            for signal in SIGNALS:
                self.assertGreaterEqual(sum(signal in r["labels"] for r in subset), 5)
        for row in rows:
            self.assertEqual(set(state(row)), {"joining_account", "group"})
            self.assertEqual(set(state(row)["joining_account"]), {"first_name", "last_name", "username", "bio"})

    def test_confusion_and_threshold_boundary(self):
        got = confusion([True, True, False, False], [.9, .1, .5, .2], .5)
        self.assertEqual([got[k] for k in ("tp", "tn", "fp", "fn")], [1, 1, 1, 1])
        self.assertEqual((got["fpr"], got["fnr"]), (.5, .5))
        self.assertEqual(confusion([True], [.85], .85)["tp"], 1)
        self.assertIsNone(confusion([True], [.9], .5)["fpr"])
        with self.assertRaises(AssertionError):
            summarize([{"id": "one", "lang": "en", "labels": []}], [])

    def test_profile_length_boundaries(self):
        base = {"id": "limit", "lang": "en", "name": "N", "bio": "", "labels": [], "why": "boundary"}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "sample.jsonl"
            for name, bio, premium, valid in [
                ("N" * 64, "b" * 70, False, True),
                ("N" * 65, "", False, False),
                ("N", "b" * 71, False, False),
                ("N", "b" * 140, True, True),
                ("N", "b" * 141, True, False),
                ("N", "🌿" * 35, False, True),
                ("N", "🌿" * 36, False, False),
            ]:
                path.write_text(json.dumps(dict(base, name=name, bio=bio, premium=premium)), encoding="utf-8")
                if valid:
                    self.assertEqual(len(read_samples(path)), 1)
                else:
                    with self.assertRaises(AssertionError):
                        read_samples(path)

    def test_language_summaries(self):
        rows = [{"id": "zh", "lang": "zh", "labels": ["promotion"]},
                {"id": "en", "lang": "en", "labels": []}]
        observations = [{"id": r["id"], "variant": v,
                         "scores": {k: .9 if r["id"] == "en" else .1 for k in SIGNALS}}
                        for r in rows for v in ("original", "short_noul")]
        result = summarize(rows, observations)
        self.assertEqual(len(result), 60)
        for m in result:
            self.assertEqual(m["tp"] + m["tn"] + m["fp"] + m["fn"], 2 if m["lang"] == "all" else 1)
            if m["signal"] == "promotion" and m["lang"] == "all":
                self.assertEqual((m["fp"], m["fn"], m["fpr"], m["fnr"]), (1, 1, 1, 1))

    def test_bad_response_is_not_a_negative_prediction(self):
        response = {"routing": {"model": "multilingual", "repo": MODEL},
                    "usage": {"truncated": False, "state_tokens_dropped": 0, "truncated_questions": []},
                    "answers": {k: {"type": "noul", "noul": .1} for k in SIGNALS}}
        self.assertEqual(len(scores(response)), 5)
        for invalid in (None, True, float("nan"), 1.1):
            bad = copy.deepcopy(response)
            bad["answers"]["scam"]["noul"] = invalid
            with self.assertRaises(AssertionError):
                scores(bad)


if __name__ == "__main__":
    unittest.main()
