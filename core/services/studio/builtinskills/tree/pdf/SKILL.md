---
name: pdf
description: Work with PDF files using the installed tools for fixed-page authoring, extraction, page operations, forms, annotations, redactions, and mechanical checks.
---

# PDF documents

> The tools named here are installed in the guest image of the sandbox add-on (gVisor or a virtual machine). The host backend does not have them: check that the import works before you rely on it.

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

A visible rectangle does not remove underlying PDF content. Redaction changes the protected page
content; overlays and annotations do not.

## References and helpers

- [Deterministic PDF quality](references/deterministic-pdf-quality.md) describes the mechanical
  verifier and the limits of extracted-geometry heuristics.
- [Content QA](references/content-qa.md) describes final-PDF text extraction.

The `scripts/` directory contains mechanical checks for openability, page boxes, text layers,
extracted content, and selected geometry. The verifier's `--strict-layout` mode is designed for known
generated layouts with predictable geometry, not arbitrary PDFs, scans, assembled pages, forms,
redactions, logos, watermarks, or background artwork.
