#!/usr/bin/env python3
"""A stand-in for the Strata engine for the backend e2e suite.

It speaks the --serve line protocol (see engine.go) and needs no model or GPU.
The answer depends on the prompt, which it decodes with the pack's vocabulary:

  * the prompt lists the get_weather tool  -> a Qwen-style tool call
  * the prompt asks for the secret word    -> a sentence that repeats the word
  * anything else                          -> a short plain answer

Usage: stub_engine.py --pack <dir> [--max-context N] [other flags are ignored] --serve
"""
import json
import pathlib
import sys
import threading

TOOL_CALL = (
    "<tool_call>\n<function=get_weather>\n<parameter=location>\nParis, France\n"
    "</parameter>\n</function>\n</tool_call>"
)
ANSWER = "Paris is the capital of France and its largest city."


def load_vocab(pack):
    vocab = json.loads((pathlib.Path(pack) / "tokenizer" / "vocab.json").read_text(encoding="utf-8"))
    # The first 256 entries are the byte tokens in byte order (see make_pack.py).
    by_id = {i: t for t, i in vocab.items()}
    return vocab, by_id


def byte_table():
    keep = list(range(33, 127)) + list(range(161, 173)) + list(range(174, 256))
    table, extra = {}, 0
    for b in range(256):
        if b in keep:
            table[b] = chr(b)
        else:
            table[b] = chr(256 + extra)
            extra += 1
    return table


def main():
    args = sys.argv[1:]
    pack, ctx = None, 4096
    for i, a in enumerate(args):
        if a == "--pack":
            pack = args[i + 1]
        elif a == "--max-context":
            ctx = int(args[i + 1])
    vocab, by_id = load_vocab(pack)
    table = byte_table()
    rev = {c: b for b, c in table.items()}
    end_id = vocab["<|im_end|>"]

    def decode(ids):
        out = bytearray()
        for i in ids:
            t = by_id.get(i, "")
            out += bytes(rev[c] for c in t) if all(c in rev for c in t) else t.encode()
        return out.decode("utf-8", "replace")

    def encode(text):
        return [vocab[table[b]] for b in text.encode("utf-8")]

    stop = threading.Event()
    lines = []
    cond = threading.Condition()

    def reader():
        for raw in sys.stdin:
            raw = raw.strip()
            if raw == "STOP":
                stop.set()
                continue
            with cond:
                lines.append(raw)
                cond.notify()
        with cond:
            lines.append("QUIT")
            cond.notify()

    threading.Thread(target=reader, daemon=True).start()
    print("INFO arch=stub", flush=True)
    print("READY %d stop" % ctx, flush=True)

    while True:
        with cond:
            while not lines:
                cond.wait()
            line = lines.pop(0)
        if line == "QUIT":
            return 0
        fields = line.split()
        if fields[0] != "GEN" or len(fields) < 3:
            print("ERR expected: GEN <max_new> [key=value ...] <id,id,...>", flush=True)
            continue
        max_new = int(fields[1])
        keys = dict(f.split("=", 1) for f in fields[2:-1] if "=" in f)
        ids = [int(x) for x in fields[-1].split(",") if x]
        if len(ids) >= ctx:
            print("ERR prompt of %d tokens does not fit the context of %d" % (len(ids), ctx), flush=True)
            continue
        lp_k = int(keys["logprobs"]) if "logprobs" in keys else -1
        prompt = decode(ids)
        if "get_weather" in prompt:
            reply = TOOL_CALL
        elif "secret word is" in prompt:
            reply = " PLATYPUS."
        else:
            reply = ANSWER
        out = encode(reply) + [end_id]
        stop.clear()
        n, finish = 0, "length"
        for tok in out:
            if n >= max_new:
                break
            if stop.is_set():
                finish = "cancel"
                break
            print("T %d" % tok, flush=True)
            if lp_k >= 0:
                alts = " ".join("%d:-%s" % (tok + a, "0.5" if a == 0 else "%d.25" % (a + 1)) for a in range(lp_k))
                print(("LP -0.5 " + alts).strip(), flush=True)
            n += 1
            if tok == end_id:
                finish = "stop"
                break
        print("DONE %d %d 1.0 1.0 %s 0 0 0" % (n, len(ids), finish), flush=True)


if __name__ == "__main__":
    sys.exit(main())
