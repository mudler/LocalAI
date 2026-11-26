#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Greedy parity between two serving arms (standard library only).

  parity.py run --url URL --model NAME --mode serial|concurrent [--n 8] [--max-tokens 64] --out run.json
  parity.py cmp A.json B.json [--margin 0.1]

run: fixed prompts, temperature 0, top-2 logprobs per token.
cmp: per stream, identical token text, or the first divergence falls where A's
top-1 vs top-2 margin is below --margin nats (a near tie). Prints the share
of identical streams and of streams that pass the margin rule.
"""
import argparse, json, sys, urllib.request, concurrent.futures as cf

PROMPTS = [
    "List three prime numbers and explain why each is prime.",
    "Write a short story about a lighthouse keeper.",
    "Explain how a bicycle stays upright.",
    "Describe the water cycle to a child.",
    "What are the main causes of inflation?",
    "Write a haiku about winter mornings.",
    "Summarize the plot of a heist movie you invent.",
    "Give step by step instructions to bake bread.",
    "Compare cats and dogs as pets.",
    "Explain what a hash table is.",
    "Write a letter to a future self.",
    "Why is the sky blue?",
]


def one(url, model, prompt, max_tokens):
    body = json.dumps(dict(model=model, prompt=prompt, max_tokens=max_tokens, temperature=0, seed=1,
                           logprobs=2, ignore_eos=True, cache_prompt=False)).encode()
    req = urllib.request.Request(url + "/v1/completions", body, {"Content-Type": "application/json"})
    r = json.loads(urllib.request.urlopen(req, timeout=900).read())
    ch = r["choices"][0]
    lp = ch.get("logprobs") or {}
    toks, margins = [], []
    if "content" in lp:  # llama-server native shape
        for t in lp["content"]:
            toks.append(t["token"])
            top = sorted((x["logprob"] for x in t.get("top_logprobs", [])), reverse=True)
            margins.append(top[0] - top[1] if len(top) > 1 else 99.0)
    else:  # OpenAI legacy completions shape
        for tok, top in zip(lp.get("tokens", []), lp.get("top_logprobs", [])):
            toks.append(tok)
            v = sorted(top.values(), reverse=True)
            margins.append(v[0] - v[1] if len(v) > 1 else 99.0)
    return dict(prompt=prompt, text=ch["text"], tokens=toks, margins=margins)


def run(a):
    prompts = (PROMPTS * 4)[: a.n]
    if a.mode == "serial":
        out = [one(a.url, a.model, p, a.max_tokens) for p in prompts]
    else:
        with cf.ThreadPoolExecutor(len(prompts)) as ex:
            out = list(ex.map(lambda p: one(a.url, a.model, p, a.max_tokens), prompts))
    json.dump(out, open(a.out, "w"))


def cmp(a):
    A, B = json.load(open(a.a)), json.load(open(a.b))
    same = ok = 0
    for x, y in zip(A, B):
        if x["text"] == y["text"]:
            same += 1; ok += 1
            continue
        ta, tb = x["tokens"], y["tokens"]
        i = next((k for k in range(min(len(ta), len(tb))) if ta[k] != tb[k]), min(len(ta), len(tb)))
        m = x["margins"][i] if i < len(x["margins"]) else 99.0
        tie = m < a.margin
        ok += tie
        print(f"diverge at token {i}: A margin {m:.3f} nats -> {'near tie, allowed' if tie else 'REAL DIFFERENCE'}")
    n = len(A)
    print(f"streams {n}: identical {same}/{n} ({100*same/n:.0f}%), pass margin rule {ok}/{n} ({100*ok/n:.0f}%)")
    sys.exit(0 if ok == n and same / n >= 0.95 else 1)


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    sp = ap.add_subparsers(dest="cmd", required=True)
    r = sp.add_parser("run"); r.add_argument("--url", required=True); r.add_argument("--model", default="m")
    r.add_argument("--mode", default="serial"); r.add_argument("--n", type=int, default=8)
    r.add_argument("--max-tokens", type=int, default=64); r.add_argument("--out", required=True)
    c = sp.add_parser("cmp"); c.add_argument("a"); c.add_argument("b"); c.add_argument("--margin", type=float, default=0.1)
    x = ap.parse_args()
    {"run": run, "cmp": cmp}[x.cmd](x)
