# Content QA

Content QA extracts text from a PDF and reports:

- empty pages with no extractable text

Command:

```bash
python scripts/content_qa.py artifact.pdf --json
```

The script does not determine semantic completeness, correctness, or whether omissions are
intentional.
