#!/usr/bin/env python3
"""Translate LocalAI react-ui locale JSON files from English (en) to Bangla (bn),
then validate that bn keys/placeholders match en.

Stdlib only. Needs internet. Backends: DeepL API (recommended, set DEEPL_API_KEY)
or Google's free public endpoint (rate limited).

Usage:
  export DEEPL_API_KEY='your-key'   # do not paste the key into the script
  python3 translate_localai_bn.py --only common.json   # test one file first
  python3 translate_localai_bn.py                 # translate + validate
  python3 translate_localai_bn.py --validate-only # validate only
  python3 translate_localai_bn.py --keep-existing # don't retranslate keys already in bn
"""
import argparse
import json
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

DEFAULT_BASE = "/root/LocalAI/core/http/react-ui/public/locales"
CACHE_FILE = Path("/tmp/localai_bn_cache.json")
DELAY = 0.6      # seconds between requests (raise with --delay if you still get 429)
_new = [0]
fresh_requests = [0]
PAUSE = 20.0     # cooldown between files (only when network requests were made)

# Terms never translated
GLOSSARY = [
    "LocalAI", "API", "GPU", "CPU", "RAM", "VRAM", "JSON", "YAML", "URL", "HTTP", "HTTPS",
    "OpenAI", "GGUF", "GGML", "LLM", "TTS", "STT", "MCP", "CUDA", "ROCm", "Docker",
    "Hugging Face", "HuggingFace", "GitHub", "Whisper", "Llama", "Stable Diffusion",
    "WebSocket", "SSE", "CORS", "ID", "UUID", "Token", "Tokens",
]

# Placeholders/markup that must survive translation unchanged
PROTECT_RE = re.compile(
    r"(\{\{[^}]*\}\}"            # {{var}} (i18next)
    r"|\$t\([^)]*\)"             # $t(key)
    r"|\{[^{}]*\}"               # {0} {name}
    r"|%\([A-Za-z_]\w*\)[sdif]"  # %(name)s
    r"|%[sdif]"                  # %s %d
    r"|</?[A-Za-z][^>]*>"        # <b>, </1>, <br/>
    r"|`[^`]*`"                  # `code`
    r"|https?://(?:[A-Za-z0-9_~/?#@$&*+=%]|[.:,;!'-](?=[A-Za-z0-9]))*)"  # URLs (ASCII only, no trailing punctuation)
)
GLOSS_RE = re.compile(
    r"(?<![A-Za-z0-9])(" + "|".join(re.escape(g) for g in sorted(GLOSSARY, key=len, reverse=True)) + r")(?![A-Za-z0-9])"
)
TOKEN_RE = re.compile(r"QQ\s*([A-Za-z]+)\s*QQ", re.I)


def tok(i):
    """index -> letters (a..z, ba, bb ...). Letters only: translators may convert digits to Bengali digits."""
    out = ""
    i += 1
    while i:
        i, r = divmod(i - 1, 26)
        out = chr(97 + r) + out
    return f"QQ{out}QQ"


def untok(letters):
    n = 0
    for ch in letters.lower():
        n = n * 26 + (ord(ch) - 96)
    return n - 1
BN_RE = re.compile(r"[\u0980-\u09FF]")


def google_translate(text):
    url = "https://translate.googleapis.com/translate_a/single"
    body = urllib.parse.urlencode(
        {"client": "gtx", "sl": "en", "tl": "bn", "dt": "t", "q": text}
    ).encode()
    req = urllib.request.Request(url, data=body, headers={"User-Agent": "Mozilla/5.0"})
    fresh_requests[0] += 1
    last = None
    for attempt in range(8):
        wait = 2 * (attempt + 1)
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                data = json.loads(r.read().decode("utf-8"))
            return "".join(seg[0] for seg in data[0] if seg and seg[0])
        except urllib.error.HTTPError as e:
            last = e
            if e.code == 429:
                ra = e.headers.get("Retry-After")
                wait = int(ra) if ra and ra.isdigit() else min(20 * (2 ** attempt), 300)
                print(f"  rate limited (429), waiting {wait}s ...", flush=True)
        except Exception as e:  # noqa: BLE001
            last = e
        time.sleep(wait)
    raise RuntimeError(f"translation request failed: {last}")


BACKEND = "google"
DEEPL_KEY = ""
ONLY = []
deepl_chars = [0]


def deepl_request(texts, extra=None):
    """Translate a list of strings in ONE request (far fewer requests -> no 429)."""
    host = "api-free.deepl.com" if DEEPL_KEY.endswith(":fx") else "api.deepl.com"
    payload = json.dumps({
        "text": texts,
        "source_lang": "EN",
        "target_lang": "BN",
        "model_type": "quality_optimized",   # Bengali is next-gen model only
        "preserve_formatting": True,
        **(extra or {}),
    }).encode("utf-8")
    req = urllib.request.Request(
        f"https://{host}/v2/translate", data=payload,
        headers={"Authorization": f"DeepL-Auth-Key {DEEPL_KEY}", "Content-Type": "application/json"},
    )
    fresh_requests[0] += 1
    deepl_chars[0] += sum(len(t) for t in texts)
    last = None
    for attempt in range(8):
        wait = 2 * (attempt + 1)
        try:
            with urllib.request.urlopen(req, timeout=90) as r:
                res = json.loads(r.read().decode("utf-8"))["translations"]
            if len(res) != len(texts):
                raise RuntimeError("DeepL returned a different number of translations")
            return [x["text"] for x in res]
        except urllib.error.HTTPError as e:
            detail = ""
            try:
                detail = e.read().decode("utf-8", "replace")[:200]
            except Exception:  # noqa: BLE001
                pass
            last = f"HTTP {e.code} {detail}"
            if e.code == 456:
                raise RuntimeError("DeepL quota exceeded (HTTP 456). Check your plan/usage.")
            if e.code in (401, 403):
                raise RuntimeError(f"DeepL rejected the key (HTTP {e.code}). Check DEEPL_API_KEY. {detail}")
            if e.code == 400:
                raise RuntimeError(f"DeepL bad request (HTTP 400): {detail}")
            if e.code == 429:
                ra = e.headers.get("Retry-After")
                wait = int(ra) if ra and ra.isdigit() else min(15 * (2 ** attempt), 180)
                print(f"  DeepL rate limit (429), waiting {wait}s ...", flush=True)
        except Exception as e:  # noqa: BLE001
            last = e
        time.sleep(wait)
    raise RuntimeError(f"DeepL request failed: {last}")


def deepl_translate(text):
    return deepl_request([text])[0]


def http_translate(text):
    return deepl_translate(text) if BACKEND == "deepl" else google_translate(text)


def mask(text):
    """Hide placeholders/terms behind QQnQQ tokens. Returns (masked, saved) or None if nothing to translate."""
    if not text.strip() or not re.search(r"[A-Za-z]", text):
        return None
    saved = []

    def stash(m):
        st, en_ = m.start(), m.end()
        before = st > 0 and m.string[st - 1].isspace()
        after = en_ < len(m.string) and m.string[en_].isspace()
        saved.append((m.group(0), before, after))
        return f" {tok(len(saved) - 1)} "

    masked = PROTECT_RE.sub(stash, text)
    masked = GLOSS_RE.sub(stash, masked)
    if not re.search(r"[A-Za-z]{2,}", re.sub(r"QQ[a-z]+QQ", "", masked)):
        return None
    return masked, saved


def unmask(text, out, saved):
    """Restore tokens; return final string or None if tokens were damaged."""
    found = sorted(untok(n) for n in TOKEN_RE.findall(out))
    if found != list(range(len(saved))):
        return None

    def rest(m):
        t, b, a = saved[untok(m.group(2))]
        return (m.group(1) if b else "") + t + (m.group(3) if a else "")

    result = re.sub(r"( ?)QQ\s*([A-Za-z]+)\s*QQ( ?)", rest, out, flags=re.I)
    if "\n" not in text:
        result = re.sub(r"[ \t]{2,}", " ", result)
    return text[: len(text) - len(text.lstrip())] + result.strip() + text[len(text.rstrip()):]


def save_cache(cache):
    CACHE_FILE.write_text(json.dumps(cache, ensure_ascii=False), encoding="utf-8")


def prewarm(strings, cache, batch=25):
    """DeepL only: translate many strings per request and fill the cache."""
    todo = []
    for t in dict.fromkeys(strings):          # unique, ordered
        if t in cache:
            continue
        m = mask(t)
        if m is None:
            cache[t] = t
        else:
            todo.append((t, m[0], m[1]))
    for i in range(0, len(todo), batch):
        chunk = todo[i:i + batch]
        outs = deepl_request([c[1] for c in chunk])
        for (orig, _masked, saved), out in zip(chunk, outs):
            res = unmask(orig, out, saved)
            if res is not None:          # damaged ones fall back to the single-string path
                cache[orig] = res
        save_cache(cache)
        print(f"  batch {min(i + batch, len(todo))}/{len(todo)} strings", flush=True)
        time.sleep(DELAY)


COMBINED_RE = re.compile(PROTECT_RE.pattern + "|" + GLOSS_RE.pattern)
X_RE = re.compile(r'<x\s+id="(\d+)"\s*(?:/>|>\s*</x>)')
FAIL_LOG = Path("/tmp/localai_bn_failures.txt")


def xml_translate(text):
    """Fallback for strings whose placeholders got damaged: DeepL XML tag handling. Returns str or None."""
    import html
    parts, saved, pos = [], [], 0
    for m in COMBINED_RE.finditer(text):
        parts.append(html.escape(text[pos:m.start()], quote=False))
        saved.append(m.group(0))
        parts.append(f'<x id="{len(saved) - 1}"/>')
        pos = m.end()
    parts.append(html.escape(text[pos:], quote=False))
    try:
        out = deepl_request(["".join(parts)], {"tag_handling": "xml", "ignore_tags": ["x"], "outline_detection": False})[0]
    except RuntimeError as e:
        if "429" in str(e) or "failed" in str(e):
            return None
        return None
    pieces = X_RE.split(out)          # [text, id, text, id, ..., text]
    ids = sorted(int(i) for i in pieces[1::2])
    if ids != list(range(len(saved))):
        return None
    built = []
    for i, piece in enumerate(pieces):
        built.append(saved[int(piece)] if i % 2 else html.unescape(piece))
    result = "".join(built)
    return text[: len(text) - len(text.lstrip())] + result.strip() + text[len(text.rstrip()):]


def translate_string(text, cache, warnings, where):
    if text in cache:
        return cache[text]
    m = mask(text)
    if m is None:
        cache[text] = text
        return text
    masked, saved = m
    result = None
    last_out = ""
    for _ in range(2):
        last_out = http_translate(masked)
        result = unmask(text, last_out, saved)
        if result is not None:
            break
        time.sleep(1)
    if result is None:
        with FAIL_LOG.open("a", encoding="utf-8") as f:
            f.write(f"{where}\n  src   : {text!r}\n  masked: {masked!r}\n  deepl : {last_out!r}\n")
        if BACKEND == "deepl":
            result = xml_translate(text)
    if result is None:
        warnings.append(f"{where}: placeholder mismatch after translation, kept English")
        result = text
    cache[text] = result
    _new[0] += 1
    if _new[0] % 10 == 0:
        save_cache(cache)
    time.sleep(DELAY)
    return result


def walk(node, fn, path=""):
    if isinstance(node, dict):
        return {k: walk(v, fn, f"{path}.{k}" if path else k) for k, v in node.items()}
    if isinstance(node, list):
        return [walk(v, fn, f"{path}[{i}]") for i, v in enumerate(node)]
    if isinstance(node, str):
        return fn(node, path)
    return node


def flatten(node, path=""):
    out = {}
    if isinstance(node, dict):
        for k, v in node.items():
            out.update(flatten(v, f"{path}.{k}" if path else k))
    elif isinstance(node, list):
        for i, v in enumerate(node):
            out.update(flatten(v, f"{path}[{i}]"))
    else:
        out[path] = node
    return out


def load_json(p):
    with open(p, encoding="utf-8") as f:
        return json.load(f)


def translate_all(en_dir, bn_dir, keep_existing):
    cache = {}
    if CACHE_FILE.exists():
        try:
            cache = json.loads(CACHE_FILE.read_text(encoding="utf-8"))
        except Exception:  # noqa: BLE001
            cache = {}
    # entries identical to the English source were fallbacks (token damage): retry them
    cache = {k: v for k, v in cache.items() if not (k == v and mask(k) is not None)}
    bn_dir.mkdir(parents=True, exist_ok=True)
    warnings = []
    files = sorted(en_dir.glob("*.json"))
    if ONLY:
        files = [f for f in files if f.name in ONLY]
        if not files:
            raise RuntimeError(f"--only matched no files in {en_dir}")
    try:
        for i, ef in enumerate(files, 1):
            en = load_json(ef)
            before = fresh_requests[0]
            existing = {}
            bf = bn_dir / ef.name
            if keep_existing and bf.exists():
                try:
                    existing = flatten(load_json(bf))
                except Exception:  # noqa: BLE001
                    existing = {}
            en_flat = flatten(en)

            def reusable(path, _ex=existing, _en=en_flat):
                return (keep_existing and isinstance(_ex.get(path), str) and _ex[path].strip()
                        and BN_RE.search(_ex[path]) and placeholders(_ex[path]) == placeholders(_en.get(path)))

            def fn(val, path, _ef=ef, _ex=existing):
                if reusable(path):
                    return _ex[path]
                return translate_string(val, cache, warnings, f"{_ef.name}:{path}")

            print(f"\n[{i}/{len(files)}] translating {ef.name} ({len(en_flat)} keys) ...", flush=True)
            if BACKEND == "deepl":
                prewarm([v for k, v in en_flat.items() if isinstance(v, str) and not reusable(k)], cache)
            out = walk(en, fn)
            bf.write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
            CACHE_FILE.write_text(json.dumps(cache, ensure_ascii=False), encoding="utf-8")
            if not validate(en_dir, bn_dir, names=[ef.name]):
                sys.exit(f"\nValidation FAILED for {ef.name}. Stopped before the next file. "
                         f"Fix it (or delete bn/{ef.name} and re-run) - later files not touched.")
            print(f"[{i}/{len(files)}] {ef.name}: validated OK", flush=True)
            if i < len(files) and fresh_requests[0] > before:
                print(f"  cooling down {PAUSE:.0f}s before next file ...", flush=True)
                time.sleep(PAUSE)
    finally:
        CACHE_FILE.write_text(json.dumps(cache, ensure_ascii=False), encoding="utf-8")
    return warnings


def placeholders(s):
    return sorted(PROTECT_RE.findall(s)) if isinstance(s, str) else []


def validate(en_dir, bn_dir, names=None):
    ok = True
    en_files = {p.name for p in en_dir.glob("*.json")}
    bn_files = {p.name for p in bn_dir.glob("*.json")} if bn_dir.exists() else set()
    if names is not None:
        en_files &= set(names)
        bn_files &= set(names)
        names = set(names)
    print("\n" + "=" * 78)
    print("VALIDATION REPORT  (en -> bn)")
    print("=" * 78)
    print(f"{'namespace':<24}{'en keys':>8}{'bn keys':>8}{'missing':>8}{'extra':>7}{'ph-bad':>7}{'untrans':>8}  status")
    print("-" * 78)
    details = []
    for name in sorted(en_files | bn_files | (names or set())):
        if name not in bn_files:
            print(f"{name:<24}{'-':>8}{'-':>8}{'ALL':>8}{'-':>7}{'-':>7}{'-':>8}  FAIL (file missing in bn)")
            ok = False
            continue
        if name not in en_files:
            print(f"{name:<24}{'-':>8}{'-':>8}{'-':>8}{'FILE':>7}{'-':>7}{'-':>8}  WARN (extra file in bn)")
            continue
        try:
            en = flatten(load_json(en_dir / name))
            bn = flatten(load_json(bn_dir / name))
        except Exception as e:  # noqa: BLE001
            print(f"{name:<24} INVALID JSON: {e}")
            ok = False
            continue
        missing = sorted(set(en) - set(bn))
        extra = sorted(set(bn) - set(en))
        ph_bad, untrans, empty, type_bad = [], [], [], []
        ph_info = {}
        for k in set(en) & set(bn):
            ev, bv = en[k], bn[k]
            if type(ev) is not type(bv):
                type_bad.append(k)
                continue
            if isinstance(ev, str):
                if placeholders(ev) != placeholders(bv):
                    ph_bad.append(k)
                    ph_info[k] = (placeholders(ev), placeholders(bv))
                if ev.strip() and not bv.strip():
                    empty.append(k)
                if ev == bv and re.search(r"[A-Za-z]{3,}", re.sub(PROTECT_RE, "", GLOSS_RE.sub("", ev))):
                    untrans.append(k)
        bad = missing or extra or ph_bad or empty or type_bad
        status = "OK" if not bad else "FAIL"
        if bad:
            ok = False
        print(f"{name:<24}{len(en):>8}{len(bn):>8}{len(missing):>8}{len(extra):>7}{len(ph_bad):>7}{len(untrans):>8}  {status}")
        for label, items in (("missing", missing), ("extra", extra), ("placeholder mismatch", ph_bad),
                             ("empty", empty), ("type mismatch", type_bad), ("still English", untrans)):
            if items:
                details.append((name, label, sorted(items), ph_info))
    if details:
        print("\nDETAILS (first 15 per category)")
        for name, label, items, info in details:
            print(f"  {name} - {label} ({len(items)}):")
            for k in items[:15]:
                print(f"      {k}")
                if label == "placeholder mismatch" and k in info:
                    print(f"          en: {info[k][0]}")
                    print(f"          bn: {info[k][1]}")
            if len(items) > 15:
                print(f"      ... +{len(items) - 15} more")
    print("-" * 78)
    print("RESULT: " + ("PASS - all bn keys match en namespaces" if ok else "FAIL - see above"))
    print("(\"untrans\" = value identical to English; often fine for names/terms, review if high)")
    return ok


def run_addons(repo, keep_existing):
    """Translate addons/**/i18n/<name>.en.json -> <name>.bn.json (same translate->validate->next flow)."""
    import shutil
    srcs = [f for f in sorted(Path(repo).glob("addons/**/*.en.json"))
            if f.parent.name == "i18n" and "node_modules" not in f.parts]
    if not srcs:
        sys.exit(f"No addons/**/i18n/*.en.json found under {repo}. Run: ls {repo}/addons/calendar/ui/src/i18n")
    stage = Path("/tmp/localai_addons_stage")
    shutil.rmtree(stage, ignore_errors=True)
    en_s, bn_s = stage / "en", stage / "bn"
    en_s.mkdir(parents=True)
    bn_s.mkdir(parents=True)
    dest = {}
    for f in srcs:
        rel = f.relative_to(Path(repo) / "addons").parts
        name = "__".join(rel)[: -len(".en.json")] + ".json"
        shutil.copy(f, en_s / name)
        d = f.with_name(f.name[: -len(".en.json")] + ".bn.json")
        dest[name] = d
        if keep_existing and d.exists():
            shutil.copy(d, bn_s / name)
    print(f"Found {len(srcs)} add-on English locale files")
    warnings = translate_all(en_s, bn_s, keep_existing)
    for name, d in dest.items():
        shutil.copy(bn_s / name, d)
        print(f"wrote {d}")
    if warnings:
        print("\nWARNINGS:")
        for w in warnings:
            print("  " + w)
    print(f"\nDone: {len(dest)} add-on bn files written.")


def main():
    global DELAY, PAUSE, BACKEND, DEEPL_KEY, ONLY, CACHE_FILE
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default=DEFAULT_BASE, help="locales directory")
    ap.add_argument("--validate-only", action="store_true")
    ap.add_argument("--backend", choices=["auto", "deepl", "google"], default="auto")
    ap.add_argument("--only", nargs="+", default=[], help="translate only these file names, e.g. common.json")
    ap.add_argument("--delay", type=float, default=None, help="seconds between requests")
    ap.add_argument("--pause", type=float, default=PAUSE, help="seconds to cool down between files")
    ap.add_argument("--addons", action="store_true", help="translate add-on UI locale files (addons/**/i18n/*.en.json)")
    ap.add_argument("--repo", default="/root/LocalAI", help="LocalAI repo root (for --addons)")
    ap.add_argument("--keep-existing", action="store_true", help="keep existing Bangla values")
    a = ap.parse_args()

    import os
    DEEPL_KEY = os.environ.get("DEEPL_API_KEY", "").strip()
    BACKEND = a.backend if a.backend != "auto" else ("deepl" if DEEPL_KEY else "google")
    if BACKEND == "deepl" and not DEEPL_KEY:
        sys.exit("Set your key first:  export DEEPL_API_KEY='your-key'")
    DELAY = a.delay if a.delay is not None else (0.4 if BACKEND == "deepl" else 0.6)
    PAUSE = a.pause if a.pause != 20.0 or BACKEND == "google" else 2.0
    ONLY = a.only
    CACHE_FILE = Path(f"/tmp/localai_bn_cache_{BACKEND}.json")
    if not a.validate_only:
        print(f"Backend: {BACKEND}")
    if a.addons:
        run_addons(a.repo, a.keep_existing)
        return
    base = Path(a.base)
    en_dir, bn_dir = base / "en", base / "bn"
    if not en_dir.is_dir():
        sys.exit(f"English directory not found: {en_dir}")

    if not a.validate_only:
        try:
            warnings = translate_all(en_dir, bn_dir, a.keep_existing)
        except (RuntimeError, KeyboardInterrupt) as e:
            sys.exit(f"\nStopped: {e}\nProgress is cached. Re-run the same command to resume.")
        if BACKEND == "deepl":
            print(f"\nDeepL characters sent this run: {deepl_chars[0]}")
        if warnings:
            print("\nWARNINGS:")
            for w in warnings:
                print("  " + w)
    ok = validate(en_dir, bn_dir)
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
