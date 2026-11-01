# Deterministic PDF Quality

The final PDF verifier is a mechanical guard for the rendered PDF package. It intentionally avoids
trying to judge professional layout from extracted PDF geometry because PDFs lose source intent and
logos, watermarks, backgrounds, OCR layers, and clipped regions can create noisy false positives.

Default `verify_pdf.py` checks:

- the PDF opens and has inspectable pages
- page dimensions are valid, with warnings for unusual or highly mixed page sizes
- extracted text/images/panels do not obviously extend outside the page box
- born-digital PDFs have an extractable text layer, with a warning when they appear image-only

Command:

```bash
python scripts/verify_pdf.py artifact.pdf --json
```

The default verifier checks mechanical PDF properties; it does not judge semantic completeness or
professional design. `--strict-layout` adds heuristic layout checks for known generated layouts and
may be noisy on arbitrary or scanned PDFs.
