"""Independent synthetic HTTP replay; run in the pinned Laya image, never the bot."""
import argparse
import copy
import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import time
import urllib.request


SHORT = {
    "promotion": ("Does this profile advertise goods or services unrelated to the group?",
                  "No unrelated advertising.", "Advertising unrelated goods or services."),
    "scam": ("Does this profile promote financial scams or gambling?",
             "No financial scam or gambling promotion.", "Promotes financial scams or gambling."),
    "adult": ("Does this profile advertise sexual content or services?",
              "No sexual advertising.", "Advertises sexual content or services."),
    "impersonation": ("Does this profile claim to be an administrator or official support?",
                      "No staff or official role claimed.", "Claims a staff or official role."),
    "ad_formatting": ("Is this profile formatted like an advertisement?",
                      "Ordinary personal profile formatting.", "Advertising style formatting."),
}


def variants(original):
    out = {"original": copy.deepcopy(original)}
    for name, total, head in [("budget_default", 1024, 256), ("budget_wide", 8192, 4096)]:
        out[name] = dict(copy.deepcopy(original), max_len=total, head_max_len=head)
    string = copy.deepcopy(original)
    for q in string["questions"].values():
        q["instructions"] = json.dumps(q["instructions"], ensure_ascii=False)
    out["json_string_instructions"] = string
    plain = copy.deepcopy(original)
    for q in plain["questions"].values():
        ins = q["instructions"]
        q["instructions"] = ins["question"] + " Inspect " + ", ".join(ins["inspect"]) + ". " + ins["focus"]
    out["plain_instructions"] = plain
    short = copy.deepcopy(original)
    for key, q in short["questions"].items():
        ins, no, yes = SHORT[key]
        q.update(instructions=ins, criteria={"false": no, "true": yes})
    out["short_noul"] = short
    for base_name in ("original", "short_noul"):
        for kind in ("choice_boolean", "choice_ab", "choice_ba", "noul_ab", "noul_reverse"):
            req = copy.deepcopy(out[base_name])
            for q in req["questions"].values():
                no, yes = q["criteria"]["false"], q["criteria"]["true"]
                if kind == "choice_boolean":
                    q.update(type="choice", criteria={"false": no, "true": yes})
                elif kind in ("choice_ab", "choice_ba"):
                    q.update(type="choice", criteria={"A": no, "B": yes} if kind == "choice_ab" else {"B": yes, "A": no})
                elif kind == "noul_ab":
                    q["labels"] = {"false": "A", "true": "B"}
                else:
                    q["option_order"] = [1, 0]
            out[base_name + "_" + kind] = req
    text = copy.deepcopy(short)
    state = text["state"]
    text["state"] = "Profile: " + "; ".join(f"{k}: {v}" for k, v in state["joining_account"].items()) + ". Group: " + json.dumps(state["group"], ensure_ascii=False)
    out["short_text_state"] = text
    return out


def positive(answer):
    if answer["type"] == "noul":
        return answer["noul"]
    probs = answer["probabilities"]
    return probs["B"] if "B" in probs else probs["true"]


def self_check():
    fixture = {"state": {"joining_account": {"bio": "synthetic"}, "group": {}},
               "questions": {k: {"type": "noul", "instructions": {"question": ins, "inspect": [], "focus": ""},
                                  "criteria": {"false": no, "true": yes}}
                             for k, (ins, no, yes) in SHORT.items()}}
    before = copy.deepcopy(fixture)
    vs = variants(fixture)
    assert fixture == before
    assert list(vs["short_noul_choice_ba"]["questions"]["scam"]["criteria"]) == ["B", "A"]
    assert positive({"type": "choice", "probabilities": {"B": .2, "A": .8}}) == .2
    assert positive({"type": "choice", "probabilities": {"false": .8, "true": .2}}) == .2
    assert positive({"type": "noul", "noul": .2}) == .2
    assert vs["original_noul_reverse"]["questions"]["scam"]["option_order"] == [1, 0]


def controls(args, snapshot, fixtures):
    """Bypass HTTP; isolate the hidden option cap without changing installed files."""
    import torch
    from laya.agent import Agent
    import laya.common as common

    torch.set_num_threads(int(os.environ["LAYA_THREADS"]))
    agent = Agent(str(snapshot), device="cpu")
    encode = common._encode_question_text

    def full_options(tok, text, **kw):
        if kw.get("truncation") is True and kw.get("max_length") == 48:
            kw = dict(kw, truncation=False)
            del kw["max_length"]
        return encode(tok, text, **kw)

    report = {"revision": os.environ["LAYA_REVISION"], "dtype": str(agent.dtype),
              "amp_enabled": agent.amp_enabled, "observations": []}

    def run(profile, name, body, uncap=False):
        common._encode_question_text = full_options if uncap else encode
        try:
            started = time.perf_counter()
            response = agent.system_one(body["state"], body["questions"],
                                        max_len=body["max_len"], head_max_len=body["head_max_len"])
            scores = {key: positive(answer) for key, answer in response["answers"].items()}
            assert all(0 <= p <= 1 for p in scores.values())
            report["observations"].append({"profile": profile, "variant": name, "request": body,
                "positive": scores, "response": response, "seconds": time.perf_counter() - started})
            args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
            print(profile, name, json.dumps(scores), flush=True)
        finally:
            common._encode_question_text = encode

    for profile, original in fixtures.items():
        run(profile, "direct_original", original)
        run(profile, "uncapped_options", original, uncap=True)
        short = variants(original)["short_noul"]
        for field in ("instructions", "criteria"):
            body = copy.deepcopy(original)
            for key, q in body["questions"].items():
                q[field] = short["questions"][key][field]
            run(profile, "short_" + field + "_only", body)
        body = copy.deepcopy(original)
        for q in body["questions"].values():
            q["criteria"] = {key: value["what"] for key, value in q["criteria"].items()}
        run(profile, "what_only", body)
        body = copy.deepcopy(short)
        for q in body["questions"].values():
            del q["criteria"]
        run(profile, "short_without_criteria", body)
        # Run one question alone to test padding / multi-question interference.
        body = copy.deepcopy(original)
        body["questions"] = {"scam": body["questions"]["scam"]}
        run(profile, "scam_alone", body)

    examples = {
        "hobby_zh": "喜欢读书、摄影和散步。",
        "hobby_en": "I enjoy reading, photography and hiking.",
        "hobby_ar": "أحب القراءة والتصوير والمشي.",
        "hobby_ru": "Люблю читать, фотографировать и гулять.",
        "sales_en": "Buy cheap Telegram members and views. DM me to order!",
        "scam_en": "Guaranteed 100x crypto profits every day. Send money now!",
        "adult_en": "Buy my nude videos. Paid sexual content, DM to subscribe.",
        "official_en": "Official Telegram Support. I am the group administrator.",
    }
    for profile, bio in examples.items():
        body = copy.deepcopy(fixtures["normal_en"])
        body["state"]["joining_account"]["bio"] = bio
        vs = variants(body)
        for name in ("short_noul", "short_noul_choice_ab", "short_noul_choice_ba"):
            run(profile, name, vs[name])
    report["complete"] = True
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fixture", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--url", default="http://127.0.0.1:8000")
    parser.add_argument("--self-check", action="store_true")
    parser.add_argument("--controls", action="store_true", help="direct Agent ablations, including diagnostic-only removal of the option cap")
    args = parser.parse_args()
    self_check()
    if args.self_check:
        print("PASS: matrix isolation and positive-label mapping")
        return
    if not args.fixture or not args.output:
        parser.error("--fixture and --output are required")
    # Use precisely the checkpoint tokenizer and encoder implementation serving HTTP.
    from huggingface_hub import snapshot_download
    from transformers import AutoTokenizer
    from laya.agent import Agent
    from laya.common import build_head, build_sequence, render_options
    import laya.common

    model = "convaiinnovations/laya-multilingual"
    revision = os.environ["LAYA_REVISION"]
    snapshot = Path(snapshot_download(model, revision=revision, local_files_only=True,
                                     allow_patterns=["tokenizer/*", "rl_agent_config.json"]))
    tok = AutoTokenizer.from_pretrained(snapshot / "tokenizer", local_files_only=True)
    fixtures = json.loads(args.fixture.read_text(encoding="utf-8"))
    if args.controls:
        controls(args, snapshot, fixtures)
        return

    def request(path, body=None):
        data = None if body is None else json.dumps(body, ensure_ascii=False).encode()
        req = urllib.request.Request(args.url.rstrip("/") + path, data=data,
                                     headers={"Authorization": "Bearer " + os.environ["LAYA_API_KEY"],
                                              "Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=120) as response:
            return json.load(response)

    health = request("/health")
    assert health["revisions"]["multilingual"] == revision, health
    assert health["checkpoint_devices"]["multilingual"] == "cpu", health
    report = {"health": health, "revision": revision,
              "packages": {p: importlib.metadata.version(p) for p in ("laya", "torch", "transformers", "tokenizers")},
              "common_py_sha256": hashlib.sha256(Path(laya.common.__file__).read_bytes()).hexdigest(),
              "checkpoint_config": json.loads((snapshot / "rl_agent_config.json").read_text()),
              "fixtures": fixtures, "variants": {}, "observations": []}
    for profile, original in fixtures.items():
        for name, body in variants(original).items():
            if profile == "normal_zh":
                report["variants"][name] = {k: v for k, v in body.items() if k != "state"}
            encoded = {}
            for key, q in body["questions"].items():
                internal = Agent._to_internal(q)
                seq, _, opts, state = build_sequence(tok, body["state"], internal,
                    body["max_len"], body["head_max_len"], option_order=q.get("option_order"),
                    return_stats=True, return_truncation_stats=True)
                head, markers, _ = build_head(tok, internal, body["head_max_len"], option_order=q.get("option_order"))
                encoded[key] = {"row_tokens": len(seq), "option_stats": opts, "state_stats": state,
                                "full_option_tokens": [len(tok(" " + s, add_special_tokens=False)["input_ids"]) for s in render_options(internal)]}
                if profile == "normal_zh":
                    encoded[key]["head_text"] = tok.decode(head)
                    encoded[key]["kept_option_tokens"] = [b - a - 1 for a, b in zip(markers, markers[1:] + [len(head) - 1])]
            started = time.perf_counter()
            response = request("/v1/systemone", body)
            elapsed = time.perf_counter() - started
            assert response["routing"]["repo"] == model, response
            scores = {key: positive(answer) for key, answer in response["answers"].items()}
            assert set(scores) == set(SHORT) and all(0 <= p <= 1 for p in scores.values())
            report["observations"].append({"profile": profile, "variant": name, "state": body["state"],
                                          "positive": scores, "seconds": elapsed, "response": response, "encoded": encoded})
            args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
            print(profile, name, json.dumps(scores), flush=True)
    report["complete"] = True
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
