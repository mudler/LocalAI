#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Trace-replay serving benchmark for an OpenAI-compatible /v1/completions endpoint.

Standard library only. Works against LocalAI and against llama-server. Used for
the paged serving A/B (see README.md in this directory).

  serve_bench.py gen --workload overcommit|mixed|steady|shared|single|natural|hol|collide|pressure [--seed 1] [--n N] [--max-tokens N]
                     [--words W] [--chats C] [--long-words L] > trace.jsonl
  serve_bench.py run --url URL --model NAME --trace trace.jsonl --arm A --rep 0 --workload W --out req.jsonl
  serve_bench.py ab  --url URL --trace trace.jsonl --workload W --reps 5 --out req.jsonl ARM=MODEL [ARM=MODEL ...]
  serve_bench.py sum req.jsonl [...] [--ref ARM] [--by-kind]

gen  writes a fixed request trace (same seed, same trace).
run  replays a trace once and appends one JSON line per request.
ab   replays a trace for several arms, interleaved: rep 0 of every arm, then
     rep 1, and so on, after one warmup request per arm switch. Each arm is a
     model name, so one LocalAI instance serves every arm (a different backend
     or different options per model).
sum  prints one row per workload and arm, with medians over reps and, with
     --ref, the bootstrap 95% confidence interval of the paired difference to
     the reference arm.
"""
import argparse, http.client, json, random, statistics, threading, time, urllib.parse

WORDS = ("alpha beta gamma delta river mountain cloud engine paper stone light window garden "
         "number signal market winter summer bridge planet circuit memory system kernel vector").split()


def text(rng, n_words):
    return " ".join(rng.choice(WORDS) for _ in range(n_words))


def gen(a):
    rng = random.Random(a.seed)
    reqs = []
    sysp = "You are a careful assistant. Answer briefly. " * 30  # about 300 tokens of shared prefix
    if a.workload == "overcommit":
        # burst: N requests of about 600 prompt tokens, all at t=0
        for i in range(a.n or 8):
            reqs.append(dict(id=i, t=0.0, kind="burst", prompt=f"Request {i}. " + text(rng, a.words),
                             max_tokens=a.max_tokens or 200))
    elif a.workload == "mixed":
        # chat streams with long outputs from t=0, one long prompt at t=3 s
        for i in range(a.chats):
            reqs.append(dict(id=i, t=0.2 * i, kind="chat", prompt=f"Chat {i}. " + text(rng, 60), max_tokens=a.chat_out))
        reqs.append(dict(id=a.chats, t=3.0, kind="long", prompt="Document. " + text(rng, a.long_words), max_tokens=32))
    elif a.workload in ("steady", "shared"):
        # Poisson arrivals of chat-sized prompts; "shared" prepends one system prompt
        t = 0.0
        for i in range(a.n or 24):
            t += rng.expovariate(a.rate)
            p = text(rng, rng.randint(40, 200))
            if a.workload == "shared":
                p = sysp + p
            reqs.append(dict(id=i, t=round(t, 3), kind="chat", prompt=p, max_tokens=rng.choice([64, 128, 256])))
    elif a.workload == "natural":
        # instruction-style prompts that end with EOS: output text like a chat answer
        # (the other traces ignore EOS, which can make a small model loop and
        # flatters n-gram drafting)
        asks = ["Explain how a bicycle stays upright.", "Write a short story about a lighthouse keeper.",
                "Describe the water cycle to a child.", "What are the main causes of inflation?",
                "Give step by step instructions to bake bread.", "Compare cats and dogs as pets.",
                "Explain what a hash table is, with an example in Python.", "Summarize the plot of a heist movie you invent.",
                "Write a polite email asking for a deadline extension.", "List ten uses for a paperclip, with one line each.",
                "Explain the difference between TCP and UDP.", "Write a Python function that parses a CSV line."]
        for i in range(a.n or 8):
            reqs.append(dict(id=i, t=0.0, kind="chat", prompt=f"User: {asks[i % len(asks)]} (request {i})\nAssistant:",
                             max_tokens=a.max_tokens or 256, eos=True))
    elif a.workload == "hol":
        # head of line: one long prompt, then short requests right behind it
        reqs.append(dict(id=0, t=0.0, kind="long", prompt="Document. " + text(rng, a.long_words), max_tokens=32))
        for i in range(1, (a.n or 8) + 1):
            reqs.append(dict(id=i, t=0.3 + 0.1 * i, kind="chat", prompt=f"Short {i}. " + text(rng, 40), max_tokens=32))
    elif a.workload == "collide":
        # cached and uncached prompts at once: warm 4 prompts one by one, then send them
        # again together with 4 new prompts of the same length
        warm = [f"Topic {i}. " + text(rng, a.words) for i in range(4)]
        for i, p in enumerate(warm):
            reqs.append(dict(id=i, t=0.0, kind="warm", prompt=p, max_tokens=8, serial=True))
        for i, p in enumerate(warm):
            reqs.append(dict(id=4 + 2 * i, t=0.0, kind="cached", prompt=p + " Again.", max_tokens=32))
            reqs.append(dict(id=5 + 2 * i, t=0.0, kind="new", prompt=f"Other {i}. " + text(rng, a.words), max_tokens=32))
    elif a.workload == "pressure":
        # conversations that come back: turn 2 repeats turn 1 and adds a question,
        # while other conversations fill the pool in between
        convs = [f"Conversation {i}. " + text(rng, a.words) for i in range(a.n or 8)]
        for i, c in enumerate(convs):
            reqs.append(dict(id=i, t=round(0.5 * i, 3), kind="turn1", prompt=c, max_tokens=64))
        for i, c in enumerate(convs):
            reqs.append(dict(id=len(convs) + i, t=round(12 + 0.5 * i, 3), kind="turn2",
                             prompt=c + " Now answer one more question: " + text(rng, 20), max_tokens=64))
    elif a.workload == "single":
        for i in range(a.n or 6):
            reqs.append(dict(id=i, t=0.0, kind="single", prompt=text(rng, 100), max_tokens=128, serial=True))
    else:
        raise SystemExit(f"unknown workload {a.workload}")
    for r in reqs:
        print(json.dumps(r))


def one(url, model, r, t0, out, lock, timeout, cache_prompt=False):
    u = urllib.parse.urlparse(url)
    body = json.dumps(dict(model=model, prompt=r["prompt"], max_tokens=r["max_tokens"], temperature=0,
                           seed=1, stream=True, ignore_eos=not r.get("eos", False), cache_prompt=cache_prompt,
                           stream_options={"include_usage": True}))
    rec = dict(id=r["id"], kind=r["kind"], t_sched=r["t"], ok=False, status=None, err=None,
               ttft=None, itl=[], n_gen=0, prompt_tokens=None, tok_ts=[])
    ts = time.perf_counter()
    rec["t_send"] = ts - t0
    try:
        c = http.client.HTTPConnection(u.hostname, u.port, timeout=timeout)
        c.request("POST", "/v1/completions", body, {"Content-Type": "application/json"})
        resp = c.getresponse()
        rec["status"] = resp.status
        if resp.status != 200:
            rec["err"] = resp.read()[:200].decode(errors="replace")
        else:
            last = None
            for line in resp:
                line = line.strip()
                if not line.startswith(b"data:"):
                    continue
                d = line[5:].strip()
                if d == b"[DONE]":
                    break
                j = json.loads(d)
                if "error" in j:
                    rec["err"] = str(j["error"])[:200]
                    break
                if j.get("usage"):
                    rec["prompt_tokens"] = j["usage"].get("prompt_tokens")
                ch = j.get("choices") or []
                if ch and ch[0].get("text"):
                    now = time.perf_counter()
                    rec["tok_ts"].append(round(now - t0, 4))
                    if last is None:
                        rec["ttft"] = now - ts
                    else:
                        rec["itl"].append(now - last)
                    last = now
                    rec["n_gen"] += 1
            rec["ok"] = rec["err"] is None and rec["n_gen"] > 0
    except Exception as e:  # noqa: BLE001 - every failure is a result here
        rec["err"] = repr(e)[:200]
    rec["t_end"] = time.perf_counter() - t0
    with lock:
        out.append(rec)


def replay(url, model, trace, timeout, cache_prompt=False):
    out, lock, th = [], threading.Lock(), []
    t0 = time.perf_counter()
    for r in trace:
        if r.get("serial"):
            one(url, model, r, t0, out, lock, timeout, cache_prompt)
            continue
        dt = r["t"] - (time.perf_counter() - t0)
        if dt > 0:
            time.sleep(dt)
        t = threading.Thread(target=one, args=(url, model, r, t0, out, lock, timeout, cache_prompt))
        t.start()
        th.append(t)
    for t in th:
        t.join()
    return sorted(out, key=lambda x: x["id"])


def write(path, recs, arm, rep, workload):
    with open(path, "a") as f:
        for rec in recs:
            rec.update(arm=arm, rep=rep, workload=workload)
            f.write(json.dumps(rec) + "\n")


def warmup(url, model):
    u = urllib.parse.urlparse(url)
    c = http.client.HTTPConnection(u.hostname, u.port, timeout=900)
    c.request("POST", "/v1/completions", json.dumps(dict(model=model, prompt="hello", max_tokens=8, temperature=0)),
              {"Content-Type": "application/json"})
    c.getresponse().read()


def run(a):
    trace = [json.loads(l) for l in open(a.trace)]
    write(a.out, replay(a.url, a.model, trace, a.timeout, a.cache_prompt), a.arm, a.rep, a.workload)


def ab(a):
    trace = [json.loads(l) for l in open(a.trace)]
    arms = [x.split("=", 1) for x in a.arms]
    for rep in range(a.reps):
        for arm, model in arms:
            warmup(a.url, model)
            time.sleep(a.pause)
            t = time.strftime("%H:%M:%S")
            write(a.out, replay(a.url, model, trace, a.timeout, a.cache_prompt), arm, rep, a.workload)
            print(f"{t} rep {rep} arm {arm} ({model}) done", flush=True)


def pct(xs, p):
    if not xs:
        return float("nan")
    xs = sorted(xs)
    k = min(len(xs) - 1, max(0, int(round(p / 100 * (len(xs) - 1)))))
    return xs[k]


def boot_ci(xs, n=2000, seed=0):
    if len(xs) < 2:
        return (float("nan"), float("nan"))
    rng = random.Random(seed)
    v = sorted(statistics.median([rng.choice(xs) for _ in xs]) for _ in range(n))
    return v[int(0.025 * n)], v[int(0.975 * n)]


def rep_metrics(rr):
    """Metrics of one rep of one arm."""
    ttft = [r["ttft"] for r in rr if r["ttft"] is not None]
    itl = [x for r in rr for x in r["itl"]]
    win = []
    for lg in (r for r in rr if r["kind"] == "long"):
        a, b = lg["t_send"], lg["t_end"]
        for r in rr:
            if r["kind"] == "chat":
                tt = r["tok_ts"]
                win += [tt[i] - tt[i - 1] for i in range(1, len(tt)) if a <= tt[i] <= b]
    span = max(r["t_end"] for r in rr) - min(r["t_send"] for r in rr)
    return dict(ok=sum(r["ok"] for r in rr), n=len(rr),
                ttft50=pct(ttft, 50), ttft99=pct(ttft, 99),
                itl50=pct(itl, 50) * 1e3, itl99=pct(itl, 99) * 1e3, itlmax=max(itl) * 1e3 if itl else float("nan"),
                gapmax=max(win) * 1e3 if win else float("nan"),
                gen_tps=sum(r["n_gen"] for r in rr) / span if span > 0 else 0.0)


COLS = [("ok", "ok/total"), ("ttft50", "TTFT p50 s"), ("ttft99", "TTFT p99 s"), ("itl50", "ITL p50 ms"),
        ("itl99", "ITL p99 ms"), ("itlmax", "ITL max ms"), ("gapmax", "chat gap max in long window ms"),
        ("gen_tps", "gen tok/s")]


def fmt(k, v):
    if v != v:
        return "-"
    return f"{v:.2f}" if k.startswith("ttft") else (f"{v:.1f}" if k == "gen_tps" else f"{v:.0f}")


def summ(a):
    rows = [json.loads(l) for f in a.files for l in open(f)]
    by = {}
    for r in rows:
        by.setdefault((r.get("workload"), r["arm"], r["rep"]), []).append(r)
    per = {}
    for (wl, arm, rep), rr in by.items():
        per.setdefault((wl, arm), {})[rep] = rep_metrics(rr)
    print("| workload | arm | reps | " + " | ".join(c[1] for c in COLS[0:1]) + " | "
          + " | ".join(f"{c[1]} (median)" for c in COLS[1:]) + (f" | {a.ref} diff: ITL p99, gap max, gen tok/s [95% CI]" if a.ref else "") + " |")
    print("|" + "---|" * (3 + len(COLS) + (1 if a.ref else 0)))
    for (wl, arm), reps in sorted(per.items()):
        ms = list(reps.values())
        ok = sum(m["ok"] for m in ms)
        tot = sum(m["n"] for m in ms)
        cells = [f"{ok}/{tot}"] + [fmt(k, statistics.median([m[k] for m in ms if m[k] == m[k]] or [float('nan')]))
                                   for k, _ in COLS[1:]]
        extra = ""
        if a.ref and (wl, a.ref) in per and arm != a.ref:
            refm = per[(wl, a.ref)]
            parts = []
            for k in ("itl99", "gapmax", "gen_tps"):
                d = [reps[r][k] - refm[r][k] for r in reps if r in refm and reps[r][k] == reps[r][k] and refm[r][k] == refm[r][k]]
                if d:
                    lo, hi = boot_ci(d)
                    parts.append(f"{fmt(k, statistics.median(d))} [{fmt(k, lo)}, {fmt(k, hi)}]")
                else:
                    parts.append("-")
            extra = " | " + "; ".join(parts)
        elif a.ref:
            extra = " | ref"
        print(f"| {wl} | {arm} | {len(ms)} | " + " | ".join(cells) + extra + " |")
    if a.by_kind:
        kinds = {}
        for r in rows:
            if r["ttft"] is not None:
                kinds.setdefault((r.get("workload"), r["arm"], r["kind"]), []).append(r["ttft"])
        print()
        print("| workload | arm | kind | n | TTFT p50 s | TTFT max s |")
        print("|---|---|---|---|---|---|")
        for (wl, arm, kind), v in sorted(kinds.items()):
            print(f"| {wl} | {arm} | {kind} | {len(v)} | {statistics.median(v):.2f} | {max(v):.2f} |")
    errs = {}
    for r in rows:
        if not r["ok"]:
            k = (r.get("workload"), r["arm"], r.get("status"), (r["err"] or "")[:80])
            errs[k] = errs.get(k, 0) + 1
    for k, v in sorted(errs.items(), key=str):
        print(f"error: workload={k[0]} arm={k[1]} status={k[2]} x{v}: {k[3]}")


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    sp = ap.add_subparsers(dest="cmd", required=True)
    g = sp.add_parser("gen")
    g.add_argument("--workload", required=True); g.add_argument("--seed", type=int, default=1)
    g.add_argument("--n", type=int, default=0); g.add_argument("--rate", type=float, default=1.0)
    g.add_argument("--chat-out", type=int, default=256); g.add_argument("--long-words", type=int, default=3600)
    g.add_argument("--max-tokens", type=int, default=0)
    g.add_argument("--words", type=int, default=600, help="overcommit: prompt words per request")
    g.add_argument("--chats", type=int, default=4, help="mixed: chat streams")
    r = sp.add_parser("run")
    r.add_argument("--url", required=True); r.add_argument("--model", default="m")
    r.add_argument("--trace", required=True); r.add_argument("--arm", required=True); r.add_argument("--rep", type=int, default=0)
    r.add_argument("--workload", default=""); r.add_argument("--out", required=True); r.add_argument("--timeout", type=float, default=900)
    r.add_argument("--cache-prompt", action="store_true", help="let the server reuse cached prompt prefixes")
    b = sp.add_parser("ab")
    b.add_argument("--url", required=True); b.add_argument("--trace", required=True); b.add_argument("--workload", required=True)
    b.add_argument("--reps", type=int, default=5); b.add_argument("--out", required=True)
    b.add_argument("--timeout", type=float, default=900); b.add_argument("--pause", type=float, default=2.0)
    b.add_argument("--cache-prompt", action="store_true", help="let the server reuse cached prompt prefixes")
    b.add_argument("arms", nargs="+", help="ARM=MODEL")
    s = sp.add_parser("sum")
    s.add_argument("files", nargs="+"); s.add_argument("--ref", default="")
    s.add_argument("--by-kind", action="store_true", help="also print TTFT per request kind")
    x = ap.parse_args()
    {"gen": gen, "run": run, "ab": ab, "sum": summ}[x.cmd](x)
