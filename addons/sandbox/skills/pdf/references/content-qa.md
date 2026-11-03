<a id="source-7cbccb7ea6593740"></a>
<!-- source: pdf/references/content-qa.md -->
# Content QA

Content QA extracts text from a PDF and reports:

- empty pages with no extractable text

Command:

```bash
python3 /usr/local/lib/localai/document-skills/pdf/content_qa.py artifact.pdf --json
```

The script does not determine semantic completeness, correctness, or whether omissions are
intentional.
