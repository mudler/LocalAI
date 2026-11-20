---
name: docx
description: Work with Microsoft Word `.docx` files using the installed `paper-docx` distribution, which imports as `docx` and extends `python-docx` with inspection, guarded edits, review structures, composition, diffs, and byte-preserving saves.
requires: [exec]
---

# DOCX with paper-docx

> The tools named here are installed in the guest image of the sandbox add-on (gVisor or a virtual machine). The host backend does not have them: check that the import works before you rely on it.

> Write the scripts you use to build or check a file in the chat's work folder, where the shell starts and that `"$WORK"` holds (the note of the turn names it too), for example `python3 "$WORK/build.py"`, or run a one-off script inline with `python3 - <<'EOF'`. Quote every path with double quotes. Save only the file the user asked for, where they asked or in their home, and nowhere else.

`paper-docx` is an upgraded distribution of `python-docx`. It keeps the familiar import and ordinary API:

```python
from docx import Document

document = Document(source_path)
```

Use your existing knowledge of `python-docx` for normal paragraphs, runs, styles, tables, sections, pictures, headers, and footers. The fork adds public APIs where ordinary `python-docx` is weak: finding text across fragmented runs and document stories, editing revisions and review structures, targeting bookmarks/fields/content controls, composing documents, inspecting package changes, and refusing operations it cannot perform safely.

`paper-docx` is installed under the `docx` import.

The [Paper API companion](references/paper-api.md#source-c4b328c0772ccf3b) maps Paper-specific capabilities to their public symbols. The [generated API reference](references/api-002.md#source-56f84baa75bfbe31) covers the complete public API, including the surface inherited from `python-docx`, with one page per module. It is large; search by symbol and read only the relevant section.

## Package behavior

Paper's targets are live. A successful text-changing replacement consumes its `Span` or `Block`; an `Anchor` is location evidence rather than a mutation target. Contextual ranking is inspection-only.

Opening a document can refuse. `paper-docx` validates the package on intake and rejects an archive with more than one reading, so a hand-rezipped or tool-mangled file may raise `MalformedPackageError` where upstream `python-docx` opened it. Saving applies the same check to its own output. Report the refusal and its message rather than working around it; each such refusal matches what Word itself does with that file. Use `docx.package.diagnose(path)` to triage a file before opening it, since it reports rather than raises.

A `docx.errors.PaperRefusal` means `paper-docx` cannot guarantee the attempted operation through its guarded API. The concrete subclass and message identify the condition that caused the refusal; `DocumentProtectedError` has a documented override in `acknowledge_protection(document)`. In rare cases an ordinary `python-docx` mutation or direct package editing may be appropriate when unguarded serialization is intentionally acceptable and its effects on the document's package and its revision, comment, and numbering structures are understood. Verify the resulting document carefully; the intake refusal above is not covered.

Precise ZIP/OOXML work is still available when it is genuinely the narrowest operation, the exact package member and schema are understood, and the edit does not cross an ownership boundary that Paper is protecting. Work on a separate candidate, preserve the source, and keep the changed-member budget explicit.

## Saving

`Document.save()` uses ordinary serialization. `docx.package.patch_save()` retains the original bytes of unchanged package parts and returns change evidence.

## Build and check a document

You cannot see a document by reading its code. Build it in the `office` sandbox profile, which has LibreOffice Writer and fonts, and look at every page before you say it is done.

1. **Switch first.** Call `sandbox_profile` with action `use` and profile `office` before you write anything. Switching restarts the sandbox: the files stay, but installed packages and running processes are lost. So switch before you build, and build the whole document in `office`. If the switch fails or `office` is not listed, say so and tell the user that the look of the document was not checked.
2. **Build it** with the library, as above, and save it.
3. **Render and look.** `sh /usr/local/lib/localai/document-skills/docx/render.sh document.docx [outdir] [dpi]` converts the document to a PDF with LibreOffice and writes `page-<n>.png` for each page (default folder: the document's name plus `-render`, default 80 dpi). It prints the paths. It stops after 180 seconds (`RENDER_TIMEOUT` changes that). Open **every** PNG with the file reader and look at it. Check that text is not cut off or overlapping, that tables and images fit the page, that headings, lists and page breaks are where you meant them to be, and that no page is empty by accident. Fix what you see, and render again.
4. **Say what you checked.** Tell the user how many pages you looked at. The render is LibreOffice's, not Word's: fonts, spacing and page breaks can differ a little (Carlito and Caladea stand in for Calibri and Cambria with the same metrics). If you could not render, say that the look was not checked. Never write that a document looks fine when you did not look at its pages.

If `render.sh` says that `soffice` is not installed, the sandbox is not in the `office` profile: switch, then build and render again, because installed packages were lost.
