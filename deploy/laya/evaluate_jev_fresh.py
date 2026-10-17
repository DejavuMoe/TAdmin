"""One frozen Jev runtime-question run on 96 newly labelled short profiles."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import time
import urllib.request

from evaluate import SIGNALS, THRESHOLDS, confusion, read_samples, state
from evaluate_jev import ROOT, MODEL, ENDPOINT
from evaluate_jev_runtime import REPO, runtime_inputs, checked_scores

DATA = ROOT / "eval/profiles-fresh96.jsonl"
FREEZE = ROOT / "eval/frozen-fresh96.json"
RISKS = tuple(s for s in SIGNALS if s != "ad_formatting")


def fresh_inputs():
    _, questions, old_sets, baseline = runtime_inputs()
    rows = read_samples(DATA)
    assert len(rows) == 96 and len({json.dumps(state(r), sort_keys=True) for r in rows}) == 96
    old_states = {json.dumps(state(r), sort_keys=True) for group in old_sets.values() for r in group}
    examples = {v.casefold() for q in questions.values() for c in q["criteria"].values() for v in c.get("examples", [])}
    for r in rows:
        assert json.dumps(state(r), sort_keys=True) not in old_states
        assert not r.get("premium") and len(r["bio"].encode("utf-16-le")) // 2 <= 70
        assert all(not v or v.casefold() not in examples for v in state(r)["joining_account"].values())
        if r["slice"] == "name_only_risk":
            assert r["bio"] == "" and r["labels"] and not r.get("username") and not r.get("last_name")
    for lang in ("zh", "en"):
        group = [r for r in rows if r["lang"] == lang]
        assert len(group) == 48 and sum(not r["labels"] for r in group) == 24
        assert {s: sum(s in r["labels"] for r in group) for s in SIGNALS} == {
            "promotion": 18, "scam": 6, "adult": 4, "impersonation": 6, "ad_formatting": 2}
    return rows, questions, baseline["returned_model"]


def summarize(rows, observations):
    index = {o["id"]: o["scores"] for o in observations}
    assert len(index) == len(observations) == len(rows) and set(index) == {r["id"] for r in rows}
    result = []
    for scope in ("all", "normal", "name_only_risk", "anti_scam", "denial_risk"):
        group = [r for r in rows if scope == "all" or (scope == "normal" and not r["labels"]) or r["slice"] == scope]
        for lang in ("all", "zh", "en"):
            selected = [r for r in group if lang == "all" or r["lang"] == lang]
            for threshold in THRESHOLDS:
                for signal in (*SIGNALS, "any_risk"):
                    truth = [bool(set(r["labels"]) & set(RISKS)) if signal == "any_risk" else signal in r["labels"] for r in selected]
                    values = [max(index[r["id"]][s] for s in RISKS) if signal == "any_risk" else index[r["id"]][signal] for r in selected]
                    counts = confusion(truth, values, threshold)
                    errors = [r["id"] for r, t, p in zip(selected, truth, values) if t != (p >= threshold)]
                    result.append(dict(scope=scope, lang=lang, threshold=threshold, signal=signal, errors=errors, **counts))
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    key = os.environ.get("TYPESAFE_API_KEY", "").strip()
    if not key:
        parser.error("set TYPESAFE_API_KEY for the local proxy")
    frozen = json.loads(FREEZE.read_text(encoding="utf-8"))
    for path, expected in frozen["files"].items():
        assert hashlib.sha256((REPO / path).read_bytes()).hexdigest() == expected, path
    rows, questions, expected_model = fresh_inputs()
    assert expected_model == frozen["expected_model"] == "jev-1.13.0"
    report = {"complete": False, "started_at": datetime.now(timezone.utc).isoformat(),
              "freeze": frozen, "endpoint": ENDPOINT, "requested_model": MODEL,
              "questions": questions, "thresholds": THRESHOLDS, "observations": []}
    with args.output.open("x", encoding="utf-8") as output:
        output.write(json.dumps(report, ensure_ascii=False, indent=2) + "\n")

    def save():
        args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    try:
        for row in rows:
            body = {"model": MODEL, "state": state(row), "questions": questions}
            request = urllib.request.Request(ENDPOINT, data=json.dumps(body, ensure_ascii=False).encode(),
                headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"})
            started = time.perf_counter()
            with urllib.request.urlopen(request, timeout=60) as response:
                raw = json.load(response)
            values = checked_scores(raw, expected_model)
            report["observations"].append({"id": row["id"], "scores": values,
                "seconds": time.perf_counter() - started, "response": raw})
            save()
            if len(report["observations"]) % 12 == 0:
                print(f"Completed {len(report['observations'])}/96 fresh profiles", flush=True)
        report["metrics"] = summarize(rows, report["observations"])
        report["complete"] = True
        report["finished_at"] = datetime.now(timezone.utc).isoformat()
        save()
    except Exception as error:
        report["failure"] = {"type": type(error).__name__, "http_status": getattr(error, "code", None)}
        save()
        raise SystemExit(f"Incomplete {len(report['observations'])}/96: {type(error).__name__}") from None
    print("PASS: frozen fresh96 completed; unchanged runtime questions and thresholds")


if __name__ == "__main__":
    main()
