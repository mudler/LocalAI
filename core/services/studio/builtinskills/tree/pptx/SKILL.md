---
name: pptx
description: Work with Microsoft PowerPoint `.pptx` files using the installed `paper-pptx` distribution, which imports as `pptx` and extends `python-pptx` with inspection, guarded edits, composition, diffs, and package-preserving saves.
---

# PPTX with paper-pptx

> The tools named here are installed in the guest image of the sandbox add-on (gVisor or a virtual machine). The host backend does not have them: check that the import works before you rely on it.

`paper-pptx` is an upgraded distribution of `python-pptx`. It keeps the familiar import and ordinary API:

```python
from pptx import Presentation

presentation = Presentation(source_path)
```

Use your existing knowledge of `python-pptx` for normal slides, shapes, text, tables, charts, images, layouts, and formatting. The fork adds public APIs for the places where ordinary `python-pptx` is thin: effective-value inspection, stable targeting, notes and footer handling, slide lifecycle, relationship-aware clone/import/composition, owned chart and media graphs, semantic diffs, and package-preserving saves.

`paper-pptx` is installed under the `pptx` import.

The [Paper API companion](references/paper-api.md) maps Paper-specific capabilities to their public symbols. The [generated API reference](references/api/index.md) covers the complete public API, including the surface inherited from `python-pptx`, with one page per module. It is large; search by symbol and read only the relevant section.

## Package behavior

Paper operations refuse ambiguous targets rather than selecting the first matching layout, placeholder, section, shape, or text block. A `presentation.batch()` block validates once at exit and discards every edit in the block if validation fails.

A `PaperRefusal` means `paper-pptx` cannot guarantee the attempted operation through its guarded API. The concrete subclass and message identify the condition that caused the refusal. In rare cases an ordinary `python-pptx` mutation or direct package editing may be appropriate when unguarded serialization is intentionally acceptable and its effects on the deck's relationship and ownership graph are understood. Verify the resulting deck carefully.

Opening a deck can also refuse. `paper-pptx` validates every member of the package on intake and rejects an archive with more than one reading or one that is unsafe to expand, so a hand-rezipped or tool-mangled file may raise `PackageLimitError` where upstream `python-pptx` opened it. Report the refusal and its message rather than working around it; each such refusal matches what PowerPoint itself does with that file.

Precise ZIP/OOXML work is still available when it is genuinely narrower than object-model mutation, the exact members and relationships are understood, and the candidate can be checked independently. Never copy a slide, chart, image, notes part, or shape XML without accounting for its complete relationship and ownership graph.

## Saving

`presentation.save(output)` uses normalized serialization and can rewrite semantically unchanged package members. `patch_save(source, presentation, output)` preserves unrelated original XML bytes and returns a diff.

Saving is refused while a `batch()` block is open, so close the block before writing.

## Not available here

Rendering a file to PDF or to images needs LibreOffice, which is not installed here. You cannot check how the file looks. Check it by opening it again with the library and reading back what you wrote, and tell the user that the look was not checked.
