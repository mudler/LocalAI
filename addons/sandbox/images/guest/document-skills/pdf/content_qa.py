#!/usr/bin/env python3
"""Extract PDF text and run deterministic content QA checks."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path


def extract_pages(pdf_path: Path) -> list[dict[str, object]]:
    import pdfplumber

    pages: list[dict[str, object]] = []
    with pdfplumber.open(str(pdf_path)) as pdf:
        for index, page in enumerate(pdf.pages, start=1):
            text = page.extract_text(layout=False) or ""
            first_line = next((line.strip() for line in text.splitlines() if line.strip()), "")
            pages.append({"index": index, "title": first_line, "text": text})
    return pages


def run_qa(pdf_path: Path) -> dict[str, object]:
    pages = extract_pages(pdf_path)
    problems: list[dict[str, object]] = []

    for page in pages:
        text = str(page["text"])
        if not text.strip():
            problems.append(
                {
                    "severity": "warning",
                    "kind": "empty_page_text",
                    "page": page["index"],
                    "message": "PDF page has no extractable text.",
                }
            )
    error_count = sum(1 for problem in problems if problem["severity"] == "error")
    warning_count = sum(1 for problem in problems if problem["severity"] == "warning")
    return {
        "ok": error_count == 0,
        "page_count": len(pages),
        "error_count": error_count,
        "warning_count": warning_count,
        "problems": problems,
        "pages": pages,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run deterministic PDF content QA.")
    parser.add_argument("pdf", type=Path)
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args(argv)

    result = run_qa(args.pdf)
    if args.json:
        print(json.dumps(result, indent=2))
    else:
        print(
            f"ok={result['ok']} pages={result['page_count']} "
            f"errors={result['error_count']} warnings={result['warning_count']}"
        )
    return 0 if result["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())
