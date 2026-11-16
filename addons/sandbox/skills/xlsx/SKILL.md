---
name: xlsx
description: Work with Excel `.xlsx` and `.xlsm` files using the installed `paper-xlsx` distribution, which imports as `openpyxl` and extends it with preservation-aware saves, guarded structural edits, diffs, and richer inspection.
requires: [exec]
---

# XLSX with paper-xlsx

> The tools named here are installed in the guest image of the sandbox add-on (gVisor or a virtual machine). The host backend does not have them: check that the import works before you rely on it.

> Write the scripts you use to build or check a file in the chat's work folder, where the shell starts and that `"$WORK"` holds (the note of the turn names it too), for example `python3 "$WORK/build.py"`, or run a one-off script inline with `python3 - <<'EOF'`. Quote every path with double quotes. Save only the file the user asked for, where they asked or in their home, and nowhere else.

`paper-xlsx` is an upgraded distribution of `openpyxl` for making precise changes to existing Excel workbooks without regenerating unrelated OOXML. Its preserve mode retains the original package, splices supported edits into place, and refuses mutations it cannot map safely. It keeps the familiar import and ordinary API:

```python
from openpyxl import load_workbook

workbook = load_workbook(source_path)
```

Use your existing knowledge of `openpyxl` for worksheets, cells, formulas, styles, tables, charts, images, names, validation, and protection. The fork adds preservation-aware package handling, targeted search and inspection, guarded structural operations with address remapping, narrow table and image mutations, recalculation and cached-value helpers, semantic validation/diffs, and edit receipts.

`paper-xlsx` is installed under the `openpyxl` import.

The [Paper API companion](references/paper-api.md#source-1d5d2fa5f7f8302a) maps Paper-specific capabilities to their public symbols. The [generated API reference](references/api-003.md#source-52f276c95ae6d391) covers the complete public API, including the surface inherited from `openpyxl`, with one page per module. It is large; search by symbol and read only the relevant section.

## Package behavior

Editable OOXML workbooks load in preserve mode by default. A `data_only=True` load does not retain formulas for saving.

A `PaperRefusal` means Paper cannot guarantee the attempted operation in preserve mode. The concrete subclass and message identify the condition that caused the refusal. In rare cases, `preserve=False` may be appropriate when stock openpyxl serialization is intentionally acceptable and its effects on the workbook's OOXML package and content are understood. Verify the resulting workbook carefully.

## Saving

`workbook.save(output)` returns `None`. In preserve mode, `receipt=True` returns change evidence:

```python
receipt = workbook.save(output_path, receipt=True)
```

Formula-affecting edits and cached-value freshness are separate. Current calculated values require recalculation.

## Recalculate and check a workbook

Paper does not calculate formulas, so a formula that you wrote or an input that you changed leaves stale or empty cached values until LibreOffice calculates them. Build the workbook in the `office` sandbox profile, which has LibreOffice Calc and fonts, then recalculate and look at the values and the pages before you say it is done.

1. **Switch first.** Call `sandbox_profile` with action `use` and profile `office` before you write anything. Switching restarts the sandbox: the files stay, but installed packages and running processes are lost. So switch before you build, and build the whole workbook in `office`. If the switch fails or `office` is not listed, say so, and do not report a computed value for a cell that depends on your edit: say that the file recalculates when it is opened in Excel or another spreadsheet program, and give the formula.
2. **Build it** with the library, as above, and save it.
3. **Recalculate.** Preferred: `openpyxl.oracle.recalc(path, output_path=new_path)`. It recalculates a temporary copy with LibreOffice and writes a copy of your workbook with the calculated values cached, and it does not deliver LibreOffice's rewritten package. Without `output_path` it only reports formula errors. Simple alternative: `sh /usr/local/lib/localai/document-skills/xlsx/recalc.sh book.xlsx [out.xlsx]`, which loads the workbook in LibreOffice and saves it again (default output: the name plus `-recalc`). That copy is written by LibreOffice and can lose styles or features it does not support, so keep the original when something is lost. Both stop after a timeout.
4. **Check the values.** Read the copy with `openpyxl.load_workbook(copy, data_only=True)` and look at the cells that depend on your formulas. Check that none is `None`, `#NAME?`, `#REF!`, `#DIV/0!` or a value that is plainly wrong. Fix the formula and recalculate again.
5. **Render and look.** `sh /usr/local/lib/localai/document-skills/xlsx/render.sh book.xlsx [outdir] [dpi]` converts the workbook to a PDF and writes `page-<n>.png` for each printed page (a wide or long sheet takes several pages; default 80 dpi, 180 seconds, `RENDER_TIMEOUT` changes that). Open the PNGs of the sheets you built and look at them: column widths (`####` means too narrow), number formats, wrapped text and charts.
6. **Say what you checked.** Tell the user which values and pages you looked at. The calculation and the render are LibreOffice's, not Excel's: a few functions differ or are missing, and the look can differ a little. A recalculated value is not proof that the formula is right for the user's purpose. If you could not recalculate, say so. Never report a value or a look that you did not check.

If a script says that `soffice` is not installed, the sandbox is not in the `office` profile: switch, then build again, because installed packages were lost. Of `openpyxl.oracle`, only `recalc` is described here; `certify`, `evaluate` and `evaluate_many` also use LibreOffice and are described in the API reference.
