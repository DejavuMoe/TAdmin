"""One frozen short-noul revision, compared on the old set and unseen synthetic profiles."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import time
import urllib.request

from diagnose import SHORT
from evaluate import MODEL, REVISION, SIGNALS, THRESHOLDS, confusion, read_samples, scores, state

ROOT = Path(__file__).parent
VARIANTS = ("short_noul", "short_noul_v2")


def questions():
    old = {k: {"type": "noul", "instructions": ins, "criteria": {"false": no, "true": yes}}
           for k, (ins, no, yes) in SHORT.items()}
    new = json.loads((ROOT / "eval/short-noul-v2.json").read_text(encoding="utf-8"))
    assert set(new) == set(old)
    assert {k for k in old if old[k] != new[k]} == {"promotion", "scam", "impersonation"}
    for q in new.values():
        assert q["type"] == "noul" and set(q["criteria"]) == {"false", "true"}
        assert all(isinstance(v, str) and v.strip() for v in [q["instructions"], *q["criteria"].values()])
    return dict(zip(VARIANTS, (old, new)))


def metrics(rows, observations):
    index = {(o["id"], o["variant"]): o["scores"] for o in observations}
    assert set(index) == {(r["id"], v) for r in rows for v in VARIANTS}
    assert len(index) == len(observations)
    result = []
    for lang in ("all", "zh", "en"):
        selected = [r for r in rows if lang == "all" or r["lang"] == lang]
        for threshold in THRESHOLDS:
            for variant in VARIANTS:
                for signal in SIGNALS:
                    counts = confusion([signal in r["labels"] for r in selected],
                                       [index[r["id"], variant][signal] for r in selected], threshold)
                    result.append(dict(lang=lang, threshold=threshold, variant=variant, signal=signal, **counts))
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--url", default="http://127.0.0.1:8000")
    args = parser.parse_args()
    frozen = json.loads((ROOT / "eval/frozen-v2.json").read_text(encoding="utf-8"))
    for path, expected in frozen["files"].items():
        assert hashlib.sha256((ROOT / path).read_bytes()).hexdigest() == expected, path
    old_freeze = json.loads((ROOT / "eval/frozen.json").read_text(encoding="utf-8"))
    assert frozen["files"]["eval/profiles.jsonl"] == old_freeze["dataset_sha256"]
    sets = {"frozen80": read_samples(ROOT / "eval/profiles.jsonl"),
            "new60": read_samples(ROOT / "eval/profiles-new-v2.jsonl")}
    question_sets = questions()
    report = {"complete": False, "freeze": frozen, "questions": question_sets,
              "thresholds": THRESHOLDS, "max_len": 2048, "head_max_len": 1024, "datasets": {}}

    def request(path, body=None):
        req = urllib.request.Request(args.url.rstrip("/") + path,
            data=None if body is None else json.dumps(body, ensure_ascii=False).encode(),
            headers={"Authorization": "Bearer " + os.environ["LAYA_API_KEY"], "Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=120) as response:
            return json.load(response)

    def save():
        args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    report["health"] = request("/health")
    assert report["health"]["revisions"]["multilingual"] == REVISION
    assert report["health"]["checkpoint_devices"]["multilingual"] == "cpu"
    save()
    for name, rows in sets.items():
        result = {"complete": False, "count": len(rows), "observations": []}
        report["datasets"][name] = result
        for i, row in enumerate(rows):
            for variant in (VARIANTS if i % 2 == 0 else VARIANTS[::-1]):
                body = {"model": MODEL, "state": state(row), "questions": question_sets[variant],
                        "max_len": 2048, "head_max_len": 1024}
                started = time.perf_counter()
                response = request("/v1/systemone", body)
                result["observations"].append({"id": row["id"], "variant": variant, "scores": scores(response),
                    "seconds": time.perf_counter() - started, "response": response})
                save()
            if (i + 1) % 10 == 0:
                print(f"{name}: {i + 1}/{len(rows)} paired profiles", flush=True)
        result["metrics"] = metrics(rows, result["observations"])
        result["complete"] = True
        save()
    report["complete"] = True
    save()
    print("PASS: both frozen datasets completed without tuning or retries")


if __name__ == "__main__":
    main()
