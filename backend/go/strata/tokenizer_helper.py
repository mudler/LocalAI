"""Tokenizer and chat-template helper for the LocalAI strata backend.

Speaks JSON lines on stdin/stdout. It reuses Strata's own Python code, so the
token ids and the rendered prompt match what the Strata server would produce:

  * tools/strata_tokenizer.py  (byte-level BPE, the reference implementation)
  * serve/frontend.py          (ChatTemplate: the model's Jinja template)

Usage: python3 -c <this script> <strata_root> <pack_dir>

Requests (one JSON object per line) and replies:

  {"op": "info"}                                  -> {"ok": true, "special_ids": {...}, "has_template": bool}
  {"op": "vocab"}                                 -> {"ok": true, "tokens": [base64 bytes, ...]}
  {"op": "encode", "text": s, "parse_special": b} -> {"ok": true, "ids": [...]}
  {"op": "template", "messages": [...], "tools": [...]|null, "kwargs": {...}}
                                                  -> {"ok": true, "prompt": s}

Any failure replies {"ok": false, "error": "..."}.
"""
import base64
import json
import pathlib
import sys


def load(strata_root, pack):
    root = pathlib.Path(strata_root)
    sys.path.insert(0, str(root))
    sys.path.insert(0, str(root / "tools"))
    import strata_tokenizer as st  # noqa: E402

    tdir = pathlib.Path(pack) / "tokenizer"
    vocab = json.loads((tdir / "vocab.json").read_text(encoding="utf-8"))
    tokens = [None] * len(vocab)
    for tok, i in vocab.items():
        tokens[i] = tok
    merges = (tdir / "merges.txt").read_text(encoding="utf-8").split("\n")
    types = json.loads((tdir / "token_type.json").read_text())
    cfg = {}
    try:
        cfg = json.loads((tdir / "tokenizer.json").read_text(encoding="utf-8"))
    except (OSError, ValueError):
        pass
    tk = st.Tokenizer(tokens, merges, types, cfg.get("pre") or "qwen35", cfg.get("special_ids") or None)

    template = None
    tpl = tdir / "chat_template.jinja"
    if tpl.exists():
        from serve import frontend  # noqa: E402

        template = frontend.ChatTemplate(tpl)
    return tk, cfg, template


def main():
    if len(sys.argv) < 3:
        print(json.dumps({"ok": False, "error": "usage: <strata_root> <pack_dir>"}), flush=True)
        return 2
    try:
        tk, cfg, template = load(sys.argv[1], sys.argv[2])
    except Exception as exc:  # noqa: BLE001 - reported to the caller as the load error
        print(json.dumps({"ok": False, "error": "load: %s: %s" % (type(exc).__name__, exc)}), flush=True)
        return 1
    print(json.dumps({"ok": True, "ready": True}), flush=True)

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
            op = req.get("op")
            if op == "info":
                out = {"special_ids": cfg.get("special_ids") or {}, "has_template": template is not None}
            elif op == "vocab":
                out = {"tokens": [base64.b64encode(tk.token_bytes(i)).decode("ascii") for i in range(len(tk.tokens))]}
            elif op == "encode":
                out = {"ids": tk.encode(req["text"], parse_special=bool(req.get("parse_special", True)))}
            elif op == "template":
                if template is None:
                    raise ValueError("the pack has no tokenizer/chat_template.jinja")
                out = {"prompt": template.render(req["messages"], tools=req.get("tools"),
                                                 add_generation_prompt=True, **(req.get("kwargs") or {}))}
            else:
                raise ValueError("unknown op %r" % (op,))
            out["ok"] = True
        except Exception as exc:  # noqa: BLE001
            out = {"ok": False, "error": "%s: %s" % (type(exc).__name__, exc)}
        print(json.dumps(out, ensure_ascii=False), flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
