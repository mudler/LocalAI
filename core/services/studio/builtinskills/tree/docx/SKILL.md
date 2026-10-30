---
name: docx
description: Work with Microsoft Word `.docx` files using the installed `paper-docx` distribution, which imports as `docx` and extends `python-docx` with inspection, guarded edits, review structures, composition, diffs, and byte-preserving saves.
requires: [exec]
---

# DOCX with paper-docx

> The tools named here are installed in the guest image of the sandbox add-on (gVisor or a virtual machine). The host backend does not have them: check that the import works before you rely on it.

`paper-docx` is an upgraded distribution of `python-docx`. It keeps the familiar import and ordinary API:

```python
from docx import Document

document = Document(source_path)
```

Use your existing knowledge of `python-docx` for normal paragraphs, runs, styles, tables, sections, pictures, headers, and footers. The fork adds public APIs where ordinary `python-docx` is weak: finding text across fragmented runs and document stories, editing revisions and review structures, targeting bookmarks/fields/content controls, composing documents, inspecting package changes, and refusing operations it cannot perform safely.

`paper-docx` is installed under the `docx` import.

The [Paper API companion](references/paper-api.md) maps Paper-specific capabilities to their public symbols. The [generated API reference](references/api/index.md) covers the complete public API, including the surface inherited from `python-docx`, with one page per module. It is large; search by symbol and read only the relevant section.

## Package behavior

Paper's targets are live. A successful text-changing replacement consumes its `Span` or `Block`; an `Anchor` is location evidence rather than a mutation target. Contextual ranking is inspection-only.

Opening a document can refuse. `paper-docx` validates the package on intake and rejects an archive with more than one reading, so a hand-rezipped or tool-mangled file may raise `MalformedPackageError` where upstream `python-docx` opened it. Saving applies the same check to its own output. Report the refusal and its message rather than working around it; each such refusal matches what Word itself does with that file. Use `docx.package.diagnose(path)` to triage a file before opening it, since it reports rather than raises.

A `docx.errors.PaperRefusal` means `paper-docx` cannot guarantee the attempted operation through its guarded API. The concrete subclass and message identify the condition that caused the refusal; `DocumentProtectedError` has a documented override in `acknowledge_protection(document)`. In rare cases an ordinary `python-docx` mutation or direct package editing may be appropriate when unguarded serialization is intentionally acceptable and its effects on the document's package and its revision, comment, and numbering structures are understood. Verify the resulting document carefully; the intake refusal above is not covered.

Precise ZIP/OOXML work is still available when it is genuinely the narrowest operation, the exact package member and schema are understood, and the edit does not cross an ownership boundary that Paper is protecting. Work on a separate candidate, preserve the source, and keep the changed-member budget explicit.

## Saving

`Document.save()` uses ordinary serialization. `docx.package.patch_save()` retains the original bytes of unchanged package parts and returns change evidence.

## Not available here

Rendering a file to PDF or to images needs LibreOffice, which is not installed here. You cannot check how the file looks. Check it by opening it again with the library and reading back what you wrote, and tell the user that the look was not checked.
