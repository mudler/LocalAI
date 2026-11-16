#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Exercise the installed PDF helper CLIs using only guest dependencies."""

import json
from pathlib import Path
import subprocess
import sys
import tempfile

from reportlab.pdfgen import canvas


HELPERS = Path("/usr/local/lib/localai/document-skills/pdf")
SUCCESS = "document-skills fixtures: ok"


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def run_helper(name, pdf, exit_code):
    result = subprocess.run(
        [sys.executable, str(HELPERS / name), str(pdf), "--json"],
        capture_output=True, text=True, timeout=30, check=False,
    )
    require(result.returncode == exit_code,
            f"{name} {pdf.name}: exit {result.returncode}, expected {exit_code}: {result.stderr}")
    data = json.loads(result.stdout)
    require(data["ok"] is (exit_code == 0), f"{name}: incorrect ok field")
    require(data["page_count"] == 1 and len(data["pages"]) == 1,
            f"{name}: incorrect page count")
    require(data["error_count"] == sum(p["severity"] == "error" for p in data["problems"]),
            f"{name}: incorrect error count")
    require(data["warning_count"] == sum(p["severity"] == "warning" for p in data["problems"]),
            f"{name}: incorrect warning count")
    return data


def check_pptx_guard(root):
    """The pptx helper must flag a collapsed deck and pass a good one."""
    helpers = HELPERS.parent / "pptx"
    for name in ("pptx_guard.py", "render.sh"):
        require((helpers / name).is_file(), f"missing guest helper asset: pptx/{name}")
    for kind, names in (("docx", ("render.sh",)), ("xlsx", ("render.sh", "recalc.sh"))):
        for name in names:
            require((HELPERS.parent / kind / name).is_file(), f"missing guest helper asset: {kind}/{name}")
    from pptx import Presentation
    from pptx.util import Inches

    decks = {}
    for name, collapsed in (("good", False), ("collapsed", True)):
        prs = Presentation()
        slide = prs.slides.add_slide(prs.slide_layouts[6])
        args = (0.5, 1.0, 4.0, 1.0) if collapsed else tuple(Inches(v) for v in (0.5, 1.0, 4.0, 1.0))
        slide.shapes.add_textbox(*args).text_frame.text = "Sandbox deck fixture"
        decks[name] = root / f"{name}.pptx"
        prs.save(decks[name])
    for name, code in (("good", 0), ("collapsed", 1)):
        result = subprocess.run([sys.executable, str(helpers / "pptx_guard.py"), str(decks[name])],
                                capture_output=True, text=True, timeout=60, check=False)
        require(result.returncode == code,
                f"pptx_guard.py {name}: exit {result.returncode}, expected {code}: {result.stdout}{result.stderr}")


def main():
    for name in ("content_qa.py", "verify_pdf.py", "notice.txt"):
        require((HELPERS / name).is_file(), f"missing guest helper asset: {name}")
    notice = (HELPERS / "notice.txt").read_text()
    for attribution in ("MIT License", "Copyright (c) 2026 Paper Instruments, Inc.",
                        "Permission is hereby granted", "THE SOFTWARE IS PROVIDED"):
        require(attribution in notice, f"missing helper license attribution: {attribution}")

    with tempfile.TemporaryDirectory(prefix="document-skills-") as directory:
        root = Path(directory)
        check_pptx_guard(root)
        for name, x in (("valid", 72), ("empty", None), ("defective", -144)):
            pdf = root / f"{name}.pdf"
            document = canvas.Canvas(str(pdf), pagesize=(612, 792), invariant=1)
            if x is not None:
                document.drawString(x, 720, "Sandbox PDF fixture")
            document.showPage()
            document.save()

        for name in ("content_qa.py", "verify_pdf.py"):
            valid = run_helper(name, root / "valid.pdf", 0)
            require(valid["error_count"] == 0 and valid["warning_count"] == 0,
                    f"{name}: valid document reported problems")
            empty = run_helper(name, root / "empty.pdf", 0)
            expected = "empty_page_text" if name == "content_qa.py" else "pdf-no-extractable-text"
            require(empty["error_count"] == 0 and empty["warning_count"] == 1,
                    f"{name}: empty-text document must warn, not fail")
            require(empty["problems"][0]["kind"] == expected, f"{name}: missing empty-text warning")
            require(empty["problems"][0]["page"] == 1, f"{name}: incorrect warning page")
            if name == "content_qa.py":
                require(valid["pages"][0]["text"].strip() == "Sandbox PDF fixture",
                        "content_qa.py: fixture text not extracted")
                require(empty["pages"][0]["text"] == "", "empty fixture contains text")
            else:
                require(valid["pages"][0]["text_line_count"] > 0,
                        "verify_pdf.py: fixture text geometry not extracted")

        # The PDF is openable and contains text, but that text is outside its
        # page box. Content QA should accept it; geometry verification must not.
        content = run_helper("content_qa.py", root / "defective.pdf", 0)
        require(content["error_count"] == 0 and content["warning_count"] == 0,
                "content_qa.py: changed content-only behavior")
        defective = run_helper("verify_pdf.py", root / "defective.pdf", 1)
        require(defective["error_count"] > 0, "verify_pdf.py: missed defective geometry")
        require(any(p["kind"] == "layout-boundary-violation" and p["severity"] == "error"
                    and p["page"] == 1 for p in defective["problems"]),
                "verify_pdf.py: missing boundary violation")
    print(SUCCESS)


if __name__ == "__main__":
    main()
