"""Paired offline evaluation of frozen synthetic Telegram profiles; no bot connection."""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import re
import time
import urllib.request

from diagnose import SHORT

SIGNALS = tuple(SHORT)
MODEL = "convaiinnovations/laya-multilingual"
REVISION = "1720e3e3357cfe1e281542e223f8273b0890ca34"
THRESHOLDS = (0.5, 0.85)


def read_samples(path):
    rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    assert rows and len({r["id"] for r in rows}) == len(rows), "empty or duplicate IDs"
    for r in rows:
        assert r["lang"] in ("zh", "en") and 1 <= len(r["name"]) <= 64
        assert len(r.get("last_name", "")) <= 64
        assert len(r["bio"].encode("utf-16-le")) // 2 <= (140 if r.get("premium", False) else 70), r["id"]
        assert not r.get("username") or re.fullmatch(r"[A-Za-z][A-Za-z0-9_]{4,31}", r["username"])
        assert len(r["labels"]) == len(set(r["labels"])) and set(r["labels"]) <= set(SIGNALS)
        assert r["why"].strip() and len(r.get("group", "")) <= 128
        assert all(v == v.strip() for v in (r["name"], r["bio"], r.get("last_name", "")))
    return rows


def state(row):
    return {"joining_account": {"first_name": row["name"], "last_name": row.get("last_name", ""),
                                "username": row.get("username", ""), "bio": row["bio"]},
            "group": {"title": row.get("group", "Go 编程交流" if row["lang"] == "zh" else "Go programming"),
                      "description": ""}}


def scores(response):
    assert response["routing"]["model"] == "multilingual" and response["routing"]["repo"] == MODEL
    usage = response["usage"]
    assert usage["truncated"] is False and usage["state_tokens_dropped"] == 0 and not usage["truncated_questions"]
    answers = response["answers"]
    assert set(answers) == set(SIGNALS)
    result = {}
    for key, answer in answers.items():
        p = answer["noul"]
        assert answer["type"] == "noul" and type(p) in (float, int) and math.isfinite(p) and 0 <= p <= 1
        result[key] = p
    return result


def confusion(truth, probabilities, threshold):
    assert len(truth) == len(probabilities) and truth
    counts = {"tp": 0, "tn": 0, "fp": 0, "fn": 0}
    for expected, p in zip(truth, probabilities):
        predicted = p >= threshold
        counts[("t" if predicted == expected else "f") + ("p" if predicted else "n")] += 1
    negatives, positives = counts["tn"] + counts["fp"], counts["tp"] + counts["fn"]
    return dict(counts, negatives=negatives, positives=positives,
                fpr=counts["fp"] / negatives if negatives else None,
                fnr=counts["fn"] / positives if positives else None)


def summarize(rows, observations):
    index = {(x["id"], x["variant"]): x["scores"] for x in observations}
    assert len(index) == len(observations) == 2 * len(rows)
    output = []
    for lang in ("all", "zh", "en"):
        selected = [r for r in rows if lang == "all" or r["lang"] == lang]
        for threshold in THRESHOLDS:
            for variant in ("original", "short_noul"):
                for signal in SIGNALS:
                    metrics = confusion([signal in r["labels"] for r in selected],
                                        [index[r["id"], variant][signal] for r in selected], threshold)
                    output.append(dict(lang=lang, threshold=threshold, variant=variant, signal=signal, **metrics))
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--samples", type=Path, required=True)
    parser.add_argument("--fixture", type=Path, required=True, help="current TestLayaExportRequests output")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--url", default="http://127.0.0.1:8000")
    args = parser.parse_args()
    rows = read_samples(args.samples)
    original = json.loads(args.fixture.read_text(encoding="utf-8"))["normal_zh"]
    assert original["model"] == MODEL and set(original["questions"]) == set(SIGNALS)
    short = {key: {"type": "noul", "instructions": ins, "criteria": {"false": no, "true": yes}}
             for key, (ins, no, yes) in SHORT.items()}
    questions = {"original": original["questions"], "short_noul": short}
    report = {"complete": False, "dataset_sha256": hashlib.sha256(args.samples.read_bytes()).hexdigest(),
              "fixture_sha256": hashlib.sha256(args.fixture.read_bytes()).hexdigest(),
              "thresholds": THRESHOLDS, "questions": questions, "max_len": 2048, "head_max_len": 1024,
              "observations": []}

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
    for i, row in enumerate(rows):
        for variant in (("original", "short_noul") if i % 2 == 0 else ("short_noul", "original")):
            body = {"model": MODEL, "state": state(row), "questions": questions[variant],
                    "max_len": 2048, "head_max_len": 1024}
            started = time.perf_counter()
            response = request("/v1/systemone", body)
            report["observations"].append({"id": row["id"], "variant": variant, "scores": scores(response),
                                          "seconds": time.perf_counter() - started, "response": response})
            save()
        if (i + 1) % 10 == 0:
            print(f"Completed {i + 1}/{len(rows)} paired profiles", flush=True)
    report["metrics"] = summarize(rows, report["observations"])
    report["complete"] = True
    save()
    print("PASS: complete paired evaluation; metrics at predeclared thresholds")


if __name__ == "__main__":
    main()
