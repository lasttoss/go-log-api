#!/usr/bin/env python3
"""End-to-end smoke test for the event ingest + rollup API.

    ingest a batch -> get a validation error back -> wait for the rollup -> read the stats

Usage: python3 scripts/smoke.py [host] [port]
"""
import json
import sys
import time
import urllib.error
import urllib.request
import uuid

HOST = sys.argv[1] if len(sys.argv) > 1 else "127.0.0.1"
PORT = int(sys.argv[2]) if len(sys.argv) > 2 else 8080
BASE = f"http://{HOST}:{PORT}"


def call(method, path, payload=None, timeout=10):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(
        BASE + path, data=data, method=method,
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as res:
            return res.status, parse(res.read().decode())
    except urllib.error.HTTPError as err:
        return err.code, parse(err.read().decode())


def parse(raw):
    """The health endpoint answers with plain text, the rest with JSON."""
    raw = raw or ""
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return {"raw": raw}


def step(name, ok, detail=""):
    print(f"  [{'PASS' if ok else 'FAIL'}] {name}{(' - ' + detail) if detail else ''}")
    if not ok:
        sys.exit(1)


def wait_until_healthy(attempts=60):
    for _ in range(attempts):
        try:
            status, _ = call("GET", "/healthz")
            if status == 200:
                return
        except OSError:
            pass
        time.sleep(1)
    sys.exit(f"FAIL: {BASE} never became healthy")


def main():
    wait_until_healthy()
    print(f"  [PASS] api is healthy - {BASE}")

    game = "cozy-garden-" + uuid.uuid4().hex[:6]
    now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    events = [
        {"game_id": game, "user_id": "u-1", "type": "login", "occurred_at": now},
        {"game_id": game, "user_id": "u-1", "type": "purchase", "value": 4.99, "occurred_at": now},
        {"game_id": game, "user_id": "u-2", "type": "login", "occurred_at": now},
        {"game_id": game, "user_id": "u-2", "type": "level_up", "occurred_at": now},
        {"game_id": game, "user_id": "u-2", "type": "level_up", "occurred_at": now},
    ]

    status, body = call("POST", "/v1/events", {"events": events})
    step("ingest accepted a batch", status == 202 and body.get("accepted") == 5, json.dumps(body))

    status, body = call("POST", "/v1/events", {"events": [{"user_id": "u-1", "type": "login"}]})
    step("a bad event is rejected with a reason",
         status == 400 and body.get("rejected") == 1 and bool(body.get("problems")),
         json.dumps(body.get("problems", [])))

    status, body = call("POST", "/v1/events", {"events": [{"game_id": "g", "user_id": "u", "type": "x"}] * 5000})
    step("an oversized batch is refused", status == 413, "HTTP %d" % status)

    deadline = time.time() + 90
    row = None
    while time.time() < deadline and row is None:
        status, body = call("GET", "/v1/games/stats?game_id=" + game)
        if status == 200 and body.get("daily"):
            row = body["daily"][-1]
            break
        time.sleep(2)

    if row is None:
        sys.exit("FAIL: the rollup never produced a row for the ingested events")
    step("rollup produced a daily row", True, json.dumps(row))
    step("the rollup counted every event", row["events"] == 5, "events=%s" % row["events"])
    step("the rollup counted distinct players", row["dau"] == 2, "dau=%s" % row["dau"])
    step("the rollup summed the value column", abs(row["value_sum"] - 4.99) < 0.001, "value_sum=%s" % row["value_sum"])

    with urllib.request.urlopen(BASE + "/metrics", timeout=10) as res:
        metrics = res.read().decode()
    step("prometheus metrics are exposed", "gamelog_rows_written_total" in metrics)

    print("OK - ingest, validation, batching, rollup and the read path all work")
    print("game_id=" + game)


if __name__ == "__main__":
    main()
