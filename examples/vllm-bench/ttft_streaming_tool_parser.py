#!/usr/bin/env python3
"""
TTFT benchmark for the vLLM backend's streaming + tool-parser path.

Three scenarios:
  1. tool_call        — request mentions a tool; model is expected to call it
  2. plain_text_short — request offers a tool but explicitly asks for ~3 sentences
  3. plain_text_long  — same as above but asks for ~8 paragraphs (1500 tokens)

The long scenario shows the dramatic difference between buffering and
streaming most clearly: with buffer-all, the client sees nothing for
20+ seconds; with native streaming, the first token arrives in <100 ms.

Usage:
  python ttft_streaming_tool_parser.py \\
      --url http://localhost:8080 --model my-coder --runs 3

The script is self-contained (stdlib only — urllib, json, time, argparse).
"""
import argparse
import json
import sys
import time
import urllib.request

DEFAULT_TOOLS = [{
    "type": "function",
    "function": {
        "name": "get_weather",
        "description": "Get current weather for a city",
        "parameters": {
            "type": "object",
            "properties": {"city": {"type": "string"}},
            "required": ["city"],
        },
    },
}]

SCENARIOS = [
    {
        "label": "tool_call",
        "messages": [{"role": "user",
                      "content": "What is the weather in Paris? Please use the tool."}],
        "max_tokens": 80,
    },
    {
        "label": "plain_text_short",
        "messages": [{"role": "user",
                      "content": "Explain in 3 short sentences what a hash table is. "
                                 "Do NOT call any tool."}],
        "max_tokens": 200,
    },
    {
        "label": "plain_text_long",
        "messages": [{"role": "user",
                      "content": "Write a thorough 8-paragraph explanation of how "
                                 "Python's GIL works, including history, current "
                                 "state, no-GIL build, and alternatives. Be "
                                 "detailed. Do NOT call any tool."}],
        "max_tokens": 1500,
    },
]


def bench_one(url, model, messages, tools, max_tokens, timeout):
    body = json.dumps({
        "model": model,
        "stream": True,
        "tools": tools,
        "messages": messages,
        "max_tokens": max_tokens,
    }).encode()
    req = urllib.request.Request(
        f"{url.rstrip('/')}/v1/chat/completions",
        data=body, headers={"Content-Type": "application/json"},
    )

    t0 = time.perf_counter()
    first_content = None
    first_tool = None
    n_content = 0
    n_tool = 0
    last = None
    finish = None
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        for line in resp:
            line = line.decode("utf-8", "replace").strip()
            if not line.startswith("data: "):
                continue
            payload = line[6:]
            if payload == "[DONE]":
                break
            try:
                chunk = json.loads(payload)
            except Exception:
                continue
            if not chunk.get("choices"):
                continue
            ch = chunk["choices"][0]
            delta = ch.get("delta") or {}
            now = time.perf_counter() - t0
            if delta.get("content"):
                if first_content is None:
                    first_content = now
                n_content += 1
            if delta.get("tool_calls"):
                if first_tool is None:
                    first_tool = now
                n_tool += 1
            if ch.get("finish_reason"):
                finish = ch["finish_reason"]
            last = now
    return {
        "ttf_content_s": first_content,
        "ttf_tool_s": first_tool,
        "n_content_chunks": n_content,
        "n_tool_chunks": n_tool,
        "total_s": last,
        "finish_reason": finish,
    }


def stats(values):
    values = [v for v in values if v is not None]
    if not values:
        return "n/a"
    return f"min={min(values):.3f}  avg={sum(values)/len(values):.3f}  max={max(values):.3f}"


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--url", default="http://localhost:8080",
                   help="LocalAI base URL (default: %(default)s)")
    p.add_argument("--model", default="coder", help="Model name (default: %(default)s)")
    p.add_argument("--runs", type=int, default=3, help="Repetitions per scenario (default: %(default)s)")
    p.add_argument("--timeout", type=int, default=180, help="Per-request timeout in seconds")
    p.add_argument("--label", default="run",
                   help="Tag for the JSON output file (default: %(default)s)")
    args = p.parse_args()

    print(f"=== TTFT Bench — {args.url}  model={args.model}  runs={args.runs} ===")
    summary = {}
    for sc in SCENARIOS:
        print(f"\nScenario: {sc['label']}")
        rows = []
        for run in range(args.runs):
            r = bench_one(args.url, args.model,
                          sc["messages"], DEFAULT_TOOLS, sc["max_tokens"], args.timeout)
            rows.append(r)
            ttf_c = f"{r['ttf_content_s']:.3f}" if r["ttf_content_s"] is not None else "—"
            ttf_t = f"{r['ttf_tool_s']:.3f}" if r["ttf_tool_s"] is not None else "—"
            print(f"  run {run+1}/{args.runs}: "
                  f"ttf_content={ttf_c}s  ttf_tool={ttf_t}s  "
                  f"n_content={r['n_content_chunks']}  n_tool={r['n_tool_chunks']}  "
                  f"total={r['total_s']:.2f}s  finish={r['finish_reason']}")
        summary[sc["label"]] = rows

    print("\n=== Summary (per scenario) ===")
    for label, rows in summary.items():
        print(f"[{label}]")
        print(f"  ttf_content_s:    {stats(r['ttf_content_s'] for r in rows)}")
        print(f"  ttf_tool_s:       {stats(r['ttf_tool_s']    for r in rows)}")
        print(f"  n_content_chunks: {stats(r['n_content_chunks'] for r in rows)}")
        print(f"  n_tool_chunks:    {stats(r['n_tool_chunks']    for r in rows)}")
        print(f"  total_s:          {stats(r['total_s']        for r in rows)}")

    out = f"ttft_bench_{args.label}.json"
    with open(out, "w") as f:
        json.dump(summary, f, indent=2)
    print(f"\nSaved to {out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
