"""Tokenizer and chat-template helper for the LocalAI strata backend.

Speaks JSON lines on stdin/stdout. It reuses Strata's own Python code, so the
token ids and the rendered prompt match what the Strata server would produce:

  * tools/strata_tokenizer.py  (byte-level BPE, the reference implementation)
  * serve/frontend.py          (ChatTemplate: the model's Jinja template)

Usage: python3 -c <this script> <strata_root> <pack_dir> [family]

family is "deepseek41" for DeepSeek V4.1-Flash, anything else (or none) for the Qwen models. For DeepSeek the
helper switches Strata's frontend to that family the way Strata's own server does (serve.frontend.use_family: the
reasoning-effort words, 1..100 included), renders the model's V4.1 chat template (serve.dsml.pick_template: the
pack's own template, the same bytes as DeepSeek's encoding.py, unless the pack carries the older spelling of the
tool-call block), writes a forced tool call in DSML, and reads the model's DSML tool calls with
serve.dsml.DsmlOutputParser. The tokenizer is the one the pack names (pre-tokenizer "deepseek3").

Requests (one JSON object per line) and replies:

  {"op": "info"}                                  -> {"ok": true, "special_ids": {...}, "has_template": bool, "family": s}
  {"op": "vocab"}                                 -> {"ok": true, "tokens": [base64 bytes, ...]}
  {"op": "encode", "text": s, "parse_special": b} -> {"ok": true, "ids": [...]}
  {"op": "template", "messages": [...], "tools": [...]|null, "kwargs": {...}}
                                                  -> {"ok": true, "prompt": s}
  {"op": "prompt", "request": {...}}              -> {"ok": true, "ids": [...], "images": [...], "thinking": b,
                                                      "force": s, "tools": [...]|null}
  {"op": "parser_new", "id": n, "thinking": b, "tools": [...], "opening": s}
                                                  -> {"ok": true, "events": [...]}
  {"op": "parser_feed", "id": n, "text": s}       -> {"ok": true, "events": [...]}
  {"op": "parser_finish", "id": n, "reason": s}   -> {"ok": true, "events": [...]}
  {"op": "parser_drop", "id": n}                  -> {"ok": true}

"prompt" takes an OpenAI chat request (messages, tools, tool_choice, chat_template_kwargs,
reasoning_effort) and does what the Strata server does before it talks to the engine: it converts the request
(serve.frontend.openai_to_messages), keeps literal control and think tags in user text as text, renders the
template and encodes the result. Image parts are returned as "images", in prompt order.

The parser ops wrap serve.frontend.OutputParser, which splits the model's text into reasoning, content and
tool calls. An event is {"kind": "reasoning"|"content"|"tool_call"|"tool_start"|"tool_args", "text": s,
"call": {"id", "name", "arguments"}|null}.

Any failure replies {"ok": false, "error": "..."}.
"""
import base64
import json
import pathlib
import sys


PARSERS = {}
FAMILY = ""


def event_json(ev):
    out = {"kind": ev.kind, "text": ev.text or ""}
    if ev.call is not None:
        out["call"] = {"id": ev.call.id, "name": ev.call.name, "arguments": ev.call.arguments}
    return out


def build_prompt(tk, template, req):
    from serve import frontend  # noqa: E402

    body = {k: req.get(k) for k in ("messages", "tools", "chat_template_kwargs", "reasoning_effort") if req.get(k) is not None}
    messages, tools, kwargs = frontend.openai_to_messages(body)
    frontend.require_messages(messages)
    choice = req.get("tool_choice")
    kind, _ = frontend.tool_choice_of(choice)
    if kind == "none":
        tools = None
    force = frontend.forced_call(choice, tools) if tools else None
    if force and FAMILY == "deepseek41":
        from serve import dsml  # noqa: E402

        force = dsml.to_dsml_force(force)
    if force:
        # The engine has no grammar. A forced call is written into the prompt, which only works without thinking.
        kwargs["enable_thinking"] = False
    thinking = kwargs.get("enable_thinking", True) is not False
    literals = frontend.literal_tags(getattr(tk, "control_tokens", ()))
    marked, marked_tools, changed = frontend.mark_think_literals(messages, tools, literals)
    prompt = template.render(marked, tools=marked_tools, **kwargs)
    plain = ()
    if changed:
        prompt, plain = frontend.unmark_think_literals(prompt, literals)
    ids = tk.encode(prompt, parse_special=True, plain=plain) if plain else tk.encode(prompt, parse_special=True)
    if force:
        ids = ids + tk.encode(force, parse_special=True)
    return {"ids": ids, "images": frontend.images_of(messages), "thinking": thinking, "force": force or "",
            "tools": tools}


def load(strata_root, pack, family=""):
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
    if family == "deepseek41":
        from serve import dsml, frontend  # noqa: E402

        frontend.use_family(family)
        # A pack whose template spells the call block the V4-Flash way gets the V4.1 one: the model would
        # read one spelling in the prompt and write another.
        tpl, swapped = dsml.pick_template(tpl)
        if swapped:
            print("strata helper: the pack's chat template is not the V4.1 one; using the V4.1 template", file=sys.stderr, flush=True)
    if tpl.exists():
        from serve import frontend  # noqa: E402

        template = frontend.ChatTemplate(tpl)
    return tk, cfg, template


def main():
    if len(sys.argv) < 3:
        print(json.dumps({"ok": False, "error": "usage: <strata_root> <pack_dir>"}), flush=True)
        return 2
    global FAMILY
    FAMILY = sys.argv[3] if len(sys.argv) > 3 else ""
    try:
        tk, cfg, template = load(sys.argv[1], sys.argv[2], FAMILY)
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
                out = {"special_ids": cfg.get("special_ids") or {}, "has_template": template is not None, "family": FAMILY}
            elif op == "vocab":
                out = {"tokens": [base64.b64encode(tk.token_bytes(i)).decode("ascii") for i in range(len(tk.tokens))]}
            elif op == "encode":
                out = {"ids": tk.encode(req["text"], parse_special=bool(req.get("parse_special", True)))}
            elif op == "prompt":
                if template is None:
                    raise ValueError("the pack has no tokenizer/chat_template.jinja")
                out = build_prompt(tk, template, req["request"])
            elif op == "parser_new":
                from serve import frontend  # noqa: E402

                parser_cls = frontend.OutputParser
                if FAMILY == "deepseek41":
                    from serve import dsml  # noqa: E402

                    parser_cls = dsml.DsmlOutputParser
                parser = parser_cls(thinking=bool(req.get("thinking", True)), tools=req.get("tools") or None,
                                               stream_tools=True, recover=True)
                PARSERS[req["id"]] = parser
                opening = req.get("opening") or ""
                out = {"events": [event_json(e) for e in (parser.feed(opening) if opening else [])]}
            elif op == "parser_feed":
                out = {"events": [event_json(e) for e in PARSERS[req["id"]].feed(req["text"])]}
            elif op == "parser_finish":
                out = {"events": [event_json(e) for e in PARSERS[req["id"]].finish(req.get("reason") or None)]}
            elif op == "parser_drop":
                PARSERS.pop(req["id"], None)
                out = {}
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
