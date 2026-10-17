"""Run inside the Laya container: synthetic data only, no Telegram connection."""
import json
import os
import urllib.request


def request(path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(
        "http://127.0.0.1:8000" + path,
        data=data,
        headers={"Authorization": "Bearer " + os.environ["LAYA_API_KEY"],
                 "Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=120) as response:
        return json.load(response)


def main():
    health = request("/health")
    assert health["checkpoint_devices"]["multilingual"] == "cpu", "not running on CPU"
    assert health["revisions"]["multilingual"] == os.environ["LAYA_REVISION"], "wrong revision"
    result = request("/v1/systemone", {
        "model": "convaiinnovations/laya-multilingual",
        "state": {"bio": "喜欢 Go 编程和摄影"},
        "questions": {"promotion": {
            "type": "noul", "instructions": "Does bio advertise goods or services?",
            "criteria": {"true": "Commercial advertising", "false": "Personal introduction"},
        }},
        "max_len": 2048, "head_max_len": 1024,
    })
    assert result["routing"]["model"] == "multilingual", "wrong checkpoint"
    assert result["routing"]["repo"] == "convaiinnovations/laya-multilingual", "wrong repository"
    assert result["usage"]["truncated"] is False, "input truncated"
    answer = result["answers"]["promotion"]
    assert answer["type"] == "noul" and 0 <= answer["noul"] <= 1, "invalid answer"
    print("PASS: pinned multilingual CPU inference; this is not an accuracy or A1 latency benchmark")


if __name__ == "__main__":
    main()
