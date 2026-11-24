---
name: pptx
description: Work with Microsoft PowerPoint `.pptx` files using the installed `paper-pptx` distribution, which imports as `pptx` and extends `python-pptx` with inspection, guarded edits, composition, diffs, and package-preserving saves. To build a deck, switch to the `office` sandbox profile, which can render every slide to an image.
requires: [exec]
---

# PPTX with paper-pptx

> The tools named here are installed in the guest image of the sandbox add-on (gVisor or a virtual machine). The host backend does not have them: check that the import works before you rely on it.

> Write the scripts you use to build or check a file in the chat's work folder, where the shell starts and that `"$WORK"` holds (the note of the turn names it too), for example `python3 "$WORK/build.py"`, or run a one-off script inline with `python3 - <<'EOF'`. Quote every path with double quotes. Save only the file the user asked for, where they asked or in their home, and nowhere else.

`paper-pptx` is an upgraded distribution of `python-pptx`. It keeps the familiar import and ordinary API:

```python
from pptx import Presentation

presentation = Presentation(source_path)
```

Use your existing knowledge of `python-pptx` for normal slides, shapes, text, tables, charts, images, layouts, and formatting. The fork adds public APIs for the places where ordinary `python-pptx` is thin: effective-value inspection, stable targeting, notes and footer handling, slide lifecycle, relationship-aware clone/import/composition, owned chart and media graphs, semantic diffs, and package-preserving saves.

`paper-pptx` is installed under the `pptx` import.

The [Paper API companion](references/paper-api.md#source-bd380984355417c8) maps Paper-specific capabilities to their public symbols. The [generated API reference](references/api-002.md#source-21810f9156652e60) covers the complete public API, including the surface inherited from `python-pptx`, with one page per module. It is large; search by symbol and read only the relevant section.

## Package behavior

Paper operations refuse ambiguous targets rather than selecting the first matching layout, placeholder, section, shape, or text block. A `presentation.batch()` block validates once at exit and discards every edit in the block if validation fails.

A `PaperRefusal` means `paper-pptx` cannot guarantee the attempted operation through its guarded API. The concrete subclass and message identify the condition that caused the refusal. In rare cases an ordinary `python-pptx` mutation or direct package editing may be appropriate when unguarded serialization is intentionally acceptable and its effects on the deck's relationship and ownership graph are understood. Verify the resulting deck carefully.

Opening a deck can also refuse. `paper-pptx` validates every member of the package on intake and rejects an archive with more than one reading or one that is unsafe to expand, so a hand-rezipped or tool-mangled file may raise `PackageLimitError` where upstream `python-pptx` opened it. Report the refusal and its message rather than working around it; each such refusal matches what PowerPoint itself does with that file.

Precise ZIP/OOXML work is still available when it is genuinely narrower than object-model mutation, the exact members and relationships are understood, and the candidate can be checked independently. Never copy a slide, chart, image, notes part, or shape XML without accounting for its complete relationship and ownership graph.

## Saving

`presentation.save(output)` uses normalized serialization and can rewrite semantically unchanged package members. `patch_save(source, presentation, output)` preserves unrelated original XML bytes and returns a diff.

Saving is refused while a `batch()` block is open, so close the block before writing.

## Build and check a deck

You cannot see a deck by reading its code. Build it in the `office` sandbox profile, which has LibreOffice Impress and fonts, and look at every slide before you say it is done.

1. **Switch first.** Call `sandbox_profile` with action `use` and profile `office` before you write anything. Switching restarts the sandbox: the files stay, but installed packages and running processes are lost. So switch before you build, and build the whole deck in `office`. If the switch fails or `office` is not listed, say so and tell the user that the look of the deck was not checked.
2. **Give sizes in EMU, not in bare numbers.** python-pptx reads a plain number as EMU (914400 per inch), so `add_textbox(0.5, 1.0, 4.0, 2.0)` makes a box of about 0.004 inch: the deck opens, and every shape is a dot. Use the guard helper for every position and size:

   ```python
   import sys
   sys.path.insert(0, "/usr/local/lib/localai/document-skills/pptx")
   from pptx_guard import inch, check

   slide.shapes.add_textbox(inch(0.5), inch(1.0), inch(4.0), inch(2.0))
   ...
   prs.save(path)
   check(path)   # raises with one line for each problem
   ```

   `check` fails when a shape is narrower or shorter than 0.2 inch (lines excepted), is outside the slide, or holds text that does not fit its box. Fix every line it prints, then run it again. It also works from the shell: `python3 /usr/local/lib/localai/document-skills/pptx/pptx_guard.py deck.pptx`. It is an estimate made without a renderer: a clean result does not prove that the deck looks good. The text check measures with the named font, or with the installed font of the same metrics (Carlito for Calibri, Caladea for Cambria, Liberation for Arial and Times New Roman). It treats a size that the file does not set as 18 pt.
3. **Render and look.** `sh /usr/local/lib/localai/document-skills/pptx/render.sh deck.pptx [outdir] [dpi]` converts the deck to a PDF with LibreOffice and writes `slide-<n>.png` for each slide (default folder: the deck's name plus `-render`, default 80 dpi). It prints the paths. It stops after 180 seconds (`RENDER_TIMEOUT` changes that). Open **every** PNG with the file reader and look at it. Check that text is not cut off or overlapping, that nothing is outside the slide, that the contrast is readable and that the slides are not empty. Fix what you see, and render again.
4. **Say what you checked.** Tell the user how many slides you looked at. The render is LibreOffice's, not PowerPoint's: spacing can differ a little. If you could not render, say that the look was not checked. Never write that a deck looks fine when you did not look at its images.

If `render.sh` says that `soffice` is not installed, the sandbox is not in the `office` profile: switch, then build and render again, because installed packages were lost.

## Other tools

The `office` profile has no ffmpeg, sox or ImageMagick. Without LibreOffice (profile `base` or `media`) you can still use `pptx_guard`, but you cannot render, so you cannot check the look: read the deck back with the library and tell the user that the look was not checked.
