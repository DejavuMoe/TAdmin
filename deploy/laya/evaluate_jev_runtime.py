"""Evaluate exported runtime questions on Jev against the frozen short-noul v1 run."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import time
import urllib.request

from evaluate import SIGNALS, THRESHOLDS, state
from evaluate_jev import ROOT, ENDPOINT, MODEL, inputs, metrics, scores

REPO = ROOT.parent.parent
BASELINE = REPO / "docs/diagnostics/jev-2026-10-18-profile-baseline.json"
FIXTURE = REPO / "docs/diagnostics/runtime-2026-10-18-requests.json"


def runtime_inputs():
    frozen, short, sets, _ = inputs()
    baseline = json.loads(BASELINE.read_text(encoding="utf-8"))
    assert baseline["complete"] is True and baseline["freeze"] == frozen
    assert baseline["questions"] == short and baseline["thresholds"] == list(THRESHOLDS)
    assert baseline["endpoint"] == ENDPOINT and baseline["requested_model"] == MODEL
    for o in baseline["observations"]:
        assert scores(o["response"]) == o["scores"]
        assert o["response"]["model"] == baseline["returned_model"]
    assert metrics(sets, baseline["observations"]) == baseline["jev_metrics"]
    fixture = json.loads(FIXTURE.read_text(encoding="utf-8"))
    original = fixture["normal_zh"]["questions"]
    assert all(r["questions"] == original for r in fixture.values())
    assert set(original) == set(SIGNALS) and all(q["type"] == "noul" for q in original.values())
    assert original != short
    # Existing Go exporter captures the shared questions; only Laya's envelope differs.
    previous = json.loads((REPO / "docs/diagnostics/laya-2026-10-18-profile-evaluation.json").read_text(encoding="utf-8"))
    assert original == previous["questions"]["original"], "runtime questions changed since the freeze"
    return frozen, original, sets, baseline


def checked_scores(response, expected_model):
    result = scores(response)
    assert response["model"] == expected_model, "model differs from the short-noul baseline"
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    key = os.environ.get("TYPESAFE_API_KEY", "").strip()
    if not key:
        parser.error("set TYPESAFE_API_KEY for the local proxy")
    frozen, questions, sets, baseline = runtime_inputs()
    paths = {"evaluator": Path(__file__), "baseline": BASELINE, "fixture": FIXTURE,
             "runtime_source": REPO / "internal/intel/questions.go"}
    report = {"complete": False, "started_at": datetime.now(timezone.utc).isoformat(),
              "endpoint": ENDPOINT, "requested_model": MODEL, "expected_model": baseline["returned_model"],
              "freeze": frozen, "sha256": {k: hashlib.sha256(p.read_bytes()).hexdigest() for k, p in paths.items()},
              "questions": questions, "short_questions": baseline["questions"], "thresholds": THRESHOLDS,
              "short_metrics": baseline["jev_metrics"], "observations": []}
    with args.output.open("x", encoding="utf-8") as output:
        output.write(json.dumps(report, ensure_ascii=False, indent=2) + "\n")

    def save():
        args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    try:
        for rows in sets.values():
            for row in rows:
                body = {"model": MODEL, "state": state(row), "questions": questions}
                request = urllib.request.Request(ENDPOINT, data=json.dumps(body, ensure_ascii=False).encode(),
                    headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"})
                started = time.perf_counter()
                with urllib.request.urlopen(request, timeout=60) as response:
                    raw = json.load(response)
                values = checked_scores(raw, baseline["returned_model"])
                report["observations"].append({"id": row["id"], "scores": values,
                    "seconds": time.perf_counter() - started, "response": raw})
                save()
                if len(report["observations"]) % 10 == 0:
                    print(f"Completed {len(report['observations'])}/140 runtime-question profiles", flush=True)
        report["runtime_metrics"] = metrics(sets, report["observations"])
        report["complete"] = True
        report["finished_at"] = datetime.now(timezone.utc).isoformat()
        save()
    except Exception as error:
        report["failure"] = {"type": type(error).__name__, "http_status": getattr(error, "code", None)}
        save()
        raise SystemExit(f"Evaluation incomplete after {len(report['observations'])}/140: {type(error).__name__}") from None
    print("PASS: 140 Jev runtime-question responses, same model as saved short-noul baseline")


if __name__ == "__main__":
    main()
