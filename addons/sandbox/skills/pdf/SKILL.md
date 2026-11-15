---
name: pdf
description: Work with PDF files using the installed tools for fixed-page authoring, extraction, page operations, forms, annotations, redactions, and mechanical checks.
requires: [exec]
---

# PDF documents

> The tools named here are installed in the guest image of the sandbox add-on (gVisor or a virtual machine). The host backend does not have them: check that the import works before you rely on it.

> Write the scripts you use to build or check a file in the chat's work folder, where the shell starts and that `"$WORK"` holds (the note of the turn names it too), for example `python3 "$WORK/build.py"`, or run a one-off script inline with `python3 - <<'EOF'`. Quote every path with double quotes. Save only the file the user asked for, where they asked or in their home, and nowhere else.

A PDF is a fixed-page package rather than a flow document. Existing PDFs generally expose pages,
regions, form fields, annotations, overlays, and redactions rather than semantic paragraph reflow.
A retained source such as DOCX, PPTX, a build script, or structured data may contain the editable
layout model for an authored PDF.

## Capabilities

- ReportLab authors PDFs from structured data and explicit page geometry.
- pypdf handles page operations, metadata, forms, and encryption.
- pdf2image uses Poppler to render pages; Pillow handles the resulting images.
- pdfplumber and pypdf extract text and tables.
- Scanned pages without a usable text layer need OCR. No OCR tool is installed here, so say that a scanned page could not be read.
- To find a PDF by its words, or to read its text, `search_files` and `read_file` work without running anything: the server reads the text of PDF, Word, Excel and PowerPoint files when they are written.

A visible rectangle does not remove underlying PDF content. Redaction changes the protected page
content; overlays and annotations do not.

## References and helpers

- [Deterministic PDF quality](references/deterministic-pdf-quality.md#source-1924639cbd4f358c) describes the mechanical
  verifier and the limits of extracted-geometry heuristics.
- [Content QA](references/content-qa.md#source-7cbccb7ea6593740) describes final-PDF text extraction.

The guest directory `/usr/local/lib/localai/document-skills/pdf/` contains mechanical checks for openability, page boxes, text layers,
extracted content, and selected geometry. The verifier's `--strict-layout` mode is designed for known
generated layouts with predictable geometry, not arbitrary PDFs, scans, assembled pages, forms,
redactions, logos, watermarks, or background artwork.
