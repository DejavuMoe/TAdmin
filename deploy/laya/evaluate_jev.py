"""Jev via the local proxy, paired with saved Laya v1 on the frozen 140 profiles."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import math
import os
from pathlib import Path
import time
import urllib.request

from diagnose import SHORT
from evaluate import SIGNALS, THRESHOLDS, confusion, read_samples, scores as laya_scores, state

ROOT = Path(__file__).resolve().parent
LAYA = ROOT.parent.parent / "docs/diagnostics/laya-2026-10-18-profile-v2.json"
ENDPOINT = "http://127.0.0.1:3425/v1/systemone"
MODEL = "typesafe/jev-latest"


def inputs():
    frozen = json.loads((ROOT / "eval/frozen-v2.json").read_text(encoding="utf-8"))
    for path, expected in frozen["files"].items():
        assert hashlib.sha256((ROOT / path).read_bytes()).hexdigest() == expected, path
    saved = json.loads(LAYA.read_text(encoding="utf-8"))
    assert saved["complete"] is True and saved["freeze"] == frozen
    questions = {k: {"type": "noul", "instructions": ins, "criteria": {"false": no, "true": yes}}
                 for k, (ins, no, yes) in SHORT.items()}
    assert questions == saved["questions"]["short_noul"]
    sets = {"frozen80": read_samples(ROOT / "eval/profiles.jsonl"),
            "new60": read_samples(ROOT / "eval/profiles-new-v2.jsonl")}
    assert [len(rows) for rows in sets.values()] == [80, 60]
    baseline = []
    for name, rows in sets.items():
        result = saved["datasets"][name]
        assert result["complete"] is True
        selected = [o for o in result["observations"] if o["variant"] == "short_noul"]
        assert len(selected) == len(rows) and {o["id"] for o in selected} == {r["id"] for r in rows}
        for o in selected:
            assert laya_scores(o["response"]) == o["scores"]
            baseline.append({"id": o["id"], "scores": o["scores"]})
    assert len({o["id"] for o in baseline}) == 140
    return frozen, questions, sets, baseline


def scores(response):
    assert isinstance(response.get("model"), str) and "jev" in response["model"].lower(), "not a Jev response"
    answers = response["answers"]
    assert set(answers) == set(SIGNALS), "missing or extra answers"
    result = {}
    for key, answer in answers.items():
        p = answer["noul"]
        assert answer["type"] == "noul" and type(p) in (float, int) and math.isfinite(p) and 0 <= p <= 1
        result[key] = p
    return result


def metrics(sets, observations):
    rows_all = [r for rows in sets.values() for r in rows]
    index = {o["id"]: o["scores"] for o in observations}
    assert len(index) == len(observations) == len(rows_all)
    assert set(index) == {r["id"] for r in rows_all}
    result = []
    for dataset, rows in {**sets, "all140": rows_all}.items():
        for lang in ("all", "zh", "en"):
            selected = [r for r in rows if lang == "all" or r["lang"] == lang]
            for threshold in THRESHOLDS:
                for signal in (*SIGNALS, "normal_any_risk"):
                    group = [r for r in selected if not r["labels"]] if signal == "normal_any_risk" else selected
                    probabilities = [max(index[r["id"]][s] for s in SIGNALS if s != "ad_formatting")
                                     if signal == "normal_any_risk" else index[r["id"]][signal] for r in group]
                    counts = confusion([signal in r["labels"] for r in group], probabilities, threshold)
                    errors = [r["id"] for r, p in zip(group, probabilities)
                              if (p >= threshold) != (signal in r["labels"])]
                    result.append(dict(dataset=dataset, lang=lang, threshold=threshold,
                                       signal=signal, errors=errors, **counts))
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    key = os.environ.get("TYPESAFE_API_KEY", "").strip()
    if not key:
        parser.error("set TYPESAFE_API_KEY for the local proxy")
    frozen, questions, sets, baseline = inputs()
    report = {"complete": False, "started_at": datetime.now(timezone.utc).isoformat(),
              "endpoint": ENDPOINT, "requested_model": MODEL, "freeze": frozen,
              "evaluator_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
              "laya_result_sha256": hashlib.sha256(LAYA.read_bytes()).hexdigest(),
              "questions": questions, "thresholds": THRESHOLDS,
              "laya_metrics": metrics(sets, baseline), "observations": []}
    # Preserve earlier evidence rather than silently replacing an existing run.
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
                values = scores(raw)
                if report["observations"]:
                    assert raw["model"] == report["returned_model"], "model changed during run"
                report["returned_model"] = raw["model"]
                report["observations"].append({"id": row["id"], "scores": values,
                    "seconds": time.perf_counter() - started, "response": raw})
                save()
                if len(report["observations"]) % 10 == 0:
                    print(f"Completed {len(report['observations'])}/140 Jev profiles", flush=True)
        report["jev_metrics"] = metrics(sets, report["observations"])
        report["complete"] = True
        report["finished_at"] = datetime.now(timezone.utc).isoformat()
        save()
    except Exception as error:
        report["failure"] = {"type": type(error).__name__, "http_status": getattr(error, "code", None)}
        save()
        raise SystemExit(f"Evaluation incomplete after {len(report['observations'])}/140: {type(error).__name__}") from None
    print("PASS: 140 Jev responses; fixed v1 questions and thresholds; no bot actions")


if __name__ == "__main__":
    main()
