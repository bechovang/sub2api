#!/usr/bin/env python3
"""Probe which Command Code models this key can actually serve via /alpha/generate.
Requires env COMMANDCODE_API_KEY (and optionally COMMANDCODE_API_BASE)."""
import json, os, subprocess, sys, uuid

KEY = os.environ["COMMANDCODE_API_KEY"]
BASE = os.environ.get("COMMANDCODE_API_BASE", "https://api.commandcode.ai")

# Models that are NOT in the sellable-open plan (documented starting set).
SKIP = ["anthropic:", "google/", "xai/", "meta/"]

def test(model):
    body = {
        "config": {
            "workingDir": "/tmp", "date": "2026-08-24",
            "environment": "win32-x64, Node.js v22.0.0",
            "structure": [], "isGitRepo": False, "currentBranch": "",
            "mainBranch": "", "gitStatus": "", "recentCommits": [],
        },
        "memory": None, "taste": None, "skills": None,
        "params": {
            "model": model,
            "messages": [{"role": "user", "content": [{"type": "text", "text": "hi"}]}],
            "tools": [], "system": "", "max_tokens": 8, "temperature": 0.3, "stream": True,
        },
        "threadId": str(uuid.uuid4()),
    }
    try:
        r = subprocess.run(
            ["curl", "-s", "-m", "30", "-N", f"{BASE}/alpha/generate",
             "-H", "Content-Type: application/json",
             "-H", f"Authorization: Bearer {KEY}",
             "-H", "x-command-code-version: 1.15.1",
             "-H", "x-cli-environment: production",
             "-H", "x-project-slug: probe",
             "-H", "x-taste-learning: true",
             "-H", "x-co-flag: false",
             "-d", json.dumps(body)],
            capture_output=True, text=True, timeout=40)
    except subprocess.TimeoutExpired:
        return False, "TIMEOUT"
    s = r.stdout.strip()
    if s.startswith("{"):
        try:
            d = json.loads(s)
            if d.get("success") is False:
                e = d.get("error", {})
                return False, f"{e.get('code')}: {str(e.get('message',''))[:60]}"
        except Exception:
            pass
    return True, "OK"

def main():
    # Fetch the /models catalog-like list from provider endpoint if reachable.
    ids = []
    try:
        r = subprocess.run(["curl", "-s", "-m", "20",
                            f"{BASE}/provider/v1/models",
                            "-H", f"Authorization: Bearer {KEY}"],
                           capture_output=True, text=True, timeout=30)
        d = json.loads(r.stdout)
        ids = [m["id"] for m in d["data"]]
    except Exception as e:
        print("could not load /models:", e)
    if not ids:
        print("No model ids; pass them on argv instead")
        return
    for m in ids:
        if any(s in m for s in SKIP) or "claude" in m:
            print(f"{m:<38} SKIP (known blocked on Go)")
            continue
        ok, info = test(m)
        print(f"{m:<38} {'OK' if ok else 'FAIL'}")
        if not ok:
            print(f"{'':40}{info}")

if __name__ == "__main__":
    main()