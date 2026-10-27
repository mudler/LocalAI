---
name: xlsx
description: Work with Excel `.xlsx` and `.xlsm` files using the installed `paper-xlsx` distribution, which imports as `openpyxl` and extends it with preservation-aware saves, guarded structural edits, diffs, and richer inspection.
---

# XLSX with paper-xlsx

`paper-xlsx` is an upgraded distribution of `openpyxl` for making precise changes to existing Excel workbooks without regenerating unrelated OOXML. Its preserve mode retains the original package, splices supported edits into place, and refuses mutations it cannot map safely. It keeps the familiar import and ordinary API:

```python
from openpyxl import load_workbook

workbook = load_workbook(source_path)
```

Use your existing knowledge of `openpyxl` for worksheets, cells, formulas, styles, tables, charts, images, names, validation, and protection. The fork adds preservation-aware package handling, targeted search and inspection, guarded structural operations with address remapping, narrow table and image mutations, recalculation and cached-value helpers, semantic validation/diffs, and edit receipts.

`paper-xlsx` is installed under the `openpyxl` import.

The [Paper API companion](references/paper-api.md) maps Paper-specific capabilities to their public symbols. The [generated API reference](references/api/index.md) covers the complete public API, including the surface inherited from `openpyxl`, with one page per module. It is large; search by symbol and read only the relevant section.

## Package behavior

Editable OOXML workbooks load in preserve mode by default. A `data_only=True` load does not retain formulas for saving.

A `PaperRefusal` means Paper cannot guarantee the attempted operation in preserve mode. The concrete subclass and message identify the condition that caused the refusal. In rare cases, `preserve=False` may be appropriate when stock openpyxl serialization is intentionally acceptable and its effects on the workbook's OOXML package and content are understood. Verify the resulting workbook carefully.

## Saving

`workbook.save(output)` returns `None`. In preserve mode, `receipt=True` returns change evidence:

```python
receipt = workbook.save(output_path, receipt=True)
```

Formula-affecting edits and cached-value freshness are separate. Current calculated values require recalculation.

## Not available here

Recalculation needs LibreOffice, which is not installed here. `openpyxl.oracle` (`recalc`, `certify`, `evaluate`, `evaluate_many`) cannot run, so do not call it. After you change a formula or an input, the cached values in the file are stale until a spreadsheet program recalculates them. Do not report a computed value for a cell that depends on your edit. Say that the file recalculates when it is opened in Excel or another spreadsheet program, and give the formula.

Rendering a file to PDF or to images needs LibreOffice, which is not installed here. You cannot check how the file looks. Check it by opening it again with the library and reading back what you wrote, and tell the user that the look was not checked.
