"""Write a tiny synthetic Strata pack (tokenizer files only) for the e2e suite.

The vocabulary is the 256 byte-level tokens plus "hi" (the only merge) and three
control tokens, so almost every byte of a text is one token. The chat template renders the
tool list into the system turn, which lets the stub engine see it.

Usage: python3 make_pack.py <pack_dir>
"""
import json
import pathlib
import sys

TEMPLATE = """{%- if tools %}<|im_start|>system
# Tools
{{ tools | tojson }}<|im_end|>
{% endif -%}
{%- for m in messages %}<|im_start|>{{ m.role }}
{{ m.content }}<|im_end|>
{% endfor -%}
{%- if add_generation_prompt %}<|im_start|>assistant
{% endif -%}"""


def byte_chars():
    # GPT-2 byte-to-unicode table: printable bytes map to themselves.
    keep = list(range(33, 127)) + list(range(161, 173)) + list(range(174, 256))
    table, extra = {}, 0
    for b in range(256):
        if b in keep:
            table[b] = chr(b)
        else:
            table[b] = chr(256 + extra)
            extra += 1
    return table


def main(out):
    tdir = pathlib.Path(out) / "tokenizer"
    tdir.mkdir(parents=True, exist_ok=True)
    table = byte_chars()
    tokens = [table[b] for b in range(256)] + ["hi", "<|im_start|>", "<|im_end|>", "<|endoftext|>"]
    (tdir / "vocab.json").write_text(json.dumps({t: i for i, t in enumerate(tokens)}), encoding="utf-8")
    # One merge, because the loader refuses an empty merge list.
    (tdir / "merges.txt").write_text("h i", encoding="utf-8")
    (tdir / "token_type.json").write_text(json.dumps([1] * 257 + [3, 3, 3]), encoding="utf-8")
    (tdir / "tokenizer.json").write_text(json.dumps({"pre": "qwen35", "special_ids": {}}), encoding="utf-8")
    (tdir / "chat_template.jinja").write_text(TEMPLATE, encoding="utf-8")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    main(sys.argv[1])
