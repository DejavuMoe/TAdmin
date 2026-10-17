import copy
import unittest

from evaluate import SIGNALS, state
from evaluate_jev import inputs, metrics, scores


class JevBaselineTests(unittest.TestCase):
    def test_frozen_inputs_and_saved_baseline(self):
        _, questions, sets, baseline = inputs()
        self.assertEqual(set(questions), set(SIGNALS))
        self.assertEqual(len(baseline), 140)
        for rows in sets.values():
            for row in rows:
                self.assertEqual(set(state(row)), {"joining_account", "group"})
        got = metrics(sets, baseline)
        self.assertEqual(len(got), 108)
        for signal, counts in {"promotion": (21, 23), "scam": (67, 1), "adult": (25, 0),
                               "impersonation": (6, 25), "ad_formatting": (83, 0),
                               "normal_any_risk": (23, 0)}.items():
            m = next(m for m in got if m["dataset"] == "all140" and m["lang"] == "all"
                     and m["threshold"] == .5 and m["signal"] == signal)
            self.assertEqual((m["fp"], m["fn"]), counts)
        with self.assertRaises(AssertionError):
            metrics(sets, baseline[:-1])
        with self.assertRaises(AssertionError):
            metrics(sets, baseline[:-1] + baseline[:1])

    def test_jev_response_validation(self):
        valid = {"model": "jev-1.13.0", "answers": {s: {"type": "noul", "noul": .5} for s in SIGNALS}}
        self.assertEqual(scores(valid), {s: .5 for s in SIGNALS})
        for value in (True, None, "0.5", float("nan"), float("inf"), -0.1, 1.1):
            bad = copy.deepcopy(valid)
            bad["answers"]["scam"]["noul"] = value
            with self.assertRaises(AssertionError):
                scores(bad)
        for change in (lambda r: r.update(model="laya"),
                       lambda r: r["answers"].pop("scam"),
                       lambda r: r["answers"]["scam"].update(type="score")):
            bad = copy.deepcopy(valid)
            change(bad)
            with self.assertRaises(AssertionError):
                scores(bad)


if __name__ == "__main__":
    unittest.main()
