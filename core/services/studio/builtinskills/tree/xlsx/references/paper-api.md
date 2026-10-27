# Paper XLSX API companion

This companion covers capabilities beyond familiar `openpyxl`. The installed distribution is `paper-xlsx`, but the import remains `openpyxl`; ordinary `openpyxl` APIs remain available.

## Imports and capability map

```python
from openpyxl import load_workbook
from openpyxl.errors import PaperRefusal
from openpyxl.preserve import (
    copy_format, diff_workbooks, receipt, scan_errors,
)
from openpyxl.package import (
    diff_cells, diff_package, xml_equivalent, xml_semantic_diff,
)
```

- `Workbook`: `search`, `validate`, and `set_pivot_refresh_on_load`.
- `Worksheet`: `allowed_values`, `append_table_row`, `replace_image`, and guarded `insert_rows`, `delete_rows`, `insert_cols`, `delete_cols`, and `move_range`.
- `Chart`: `repoint(series_index, new_range)` for one series' validated, sheet-qualified values range.
- `openpyxl.preserve`: inspection, formatting helpers, workbook diff, and standalone receipt.
- `openpyxl.package`: cell, part, and semantic XML comparison.
- `openpyxl.oracle`: needs LibreOffice, which is not installed here. Do not call it. `openpyxl.errors` provides `PaperRefusal` and typed subclasses.

## Preservation and inspection

```python
wb = load_workbook(source_path)
sheets = wb.sheetnames
hits = wb.search("Revenue", formulas=False)
choices = wb["Inputs"].allowed_values("B7")
```

`load_workbook(..., preserve=None)` enables preserve mode by default for editable OOXML workbooks;
`preserve=False` requests the upstream-compatible path, and read-only loads never use preserve
mode. `wb.preserve` reports the selected mode. Preserve mode keeps the original package as the
source of truth, splices modeled edits into touched parts, and keeps untouched parts byte-identical.
Malformed or ambiguous packages refuse at load; Paper imposes no fixed archive-size, part-count,
part-size, aggregate-size, or compression-ratio caps.

A preserve save from `data_only=True` refuses unless
`wb.save(path, allow_formula_loss=True)` explicitly accepts formula loss in edited cells; untouched
source formulas remain preserved. Formula-affecting edits can intentionally invalidate stale
cached results.

Preservation checks run during load, mutation, validation, and save.

`search(text_or_regex, regex=False, values=True, formulas=True)` returns dictionaries with
`address`, `match`, and `kind` (`"value"` or `"formula"`).
`allowed_values(cell)` returns exact values for a literal list or deterministic static
one-dimensional range, returns `None` only when no list validation covers the cell, and refuses
unsupported or ambiguous sources rather than guessing.

## Guarded edits

```python
remap = wb["Model"].insert_rows(5, amount=2)
new_address = remap.map("Model!B7")  # "Model!B9"; deleted addresses map to None

chart = wb["Dashboard"]._charts[0]
chart.repoint(0, "'Model'!$B$2:$B$13")
```

Under preserve mode, row/column insertions and deletions rewrite supported formulas, names, tables,
chart references, and drawing anchors, then return `openpyxl.preserve.AddressRemap`. `move_range`
and supported sheet rename/removal paths are guarded but return no remap. Unsafe dynamic references,
destructive table/chart edits, or unsupported relationships refuse before operation mutation.

Paper also hardens ordinary cell and number-format assignment, `Worksheet.append()`, merge/unmerge,
sheet rename, `Workbook.remove()`, and `Workbook.move_sheet()`. Normal worksheet creation is
supported; adding a chartsheet to a preserved package refuses. Chart and image insertion is
supported on new sheets and compatible loaded drawings; unsupported insertion refuses at call time
before adding the object to the model.

`copy_format(ws, "B2", "C2:F2")` atomically copies font, fill, border, alignment, number format,
and protection, returning the number of destination cells. It does not copy values, formulas,
comments, hyperlinks, validation, row heights, or column widths. Merged interiors and strictly
protected destinations refuse. Ordinary openpyxl properties cover explicit formatting, metadata,
comments, protection, and worksheet visibility. Raw package mutation is outside the supported
contract.

## Validate, diff, and save

```python
wb.validate()  # full preserve-save validation; writes nothing; returns None
edit_receipt = wb.save(output_path, receipt=True)
change_report = diff_workbooks(source_path, output_path, remaps=(remap,))
cell_changes = diff_cells(source_path, output_path)
package_changes = diff_package(source_path, output_path, max_detail=25)
```

Normal `wb.save(path)` returns `None`. `receipt=True` is preserve-only and returns an `EditReceipt`
cumulative from the as-loaded source, not just the latest save. Standalone
`receipt(before, after)` builds a receipt from two saved files. Receipts and diffs expose
versioned `.to_dict()` payloads; receipt schema v2 separates requested changes from automatic
`derived_effects`, such as cache invalidation, recalculation metadata, pivot refresh, and package
relationship changes. `scan_errors(wb)` reports cached errors and tokenized formula error operands,
not error-like text inside strings; formulas it cannot tokenize refuse instead of returning a
partial report.

`diff_cells(a, b)` returns a `CellsDiff` with `.clean`, cell changes, and added/removed sheets; it
does not report style-only changes. `diff_package(a, b, max_detail=25)` returns a `PackageDiff` of
added, removed, byte-identical, semantically equivalent, and changed parts.
`xml_semantic_diff(a, b, max_diffs=25)` returns human-readable XML differences, while
`xml_equivalent(a, b)` returns a boolean. Inputs may be paths, bytes, or binary file-like objects.

## Recalculation and caches

Paper does not calculate formulas in Python. Recalculation, certification and scenarios (`openpyxl.oracle`) need LibreOffice, which is not installed here, so they cannot run. Cached values of cells that depend on an edit stay stale until a spreadsheet program recalculates the file.

## Tables, images, pivots, and outcomes

`append_table_row(table_name, values)` atomically expands a supported named table from a sequence
or header-keyed mapping, preserves totals, styles, and formats, and derives formulas only from
declared calculated-column metadata. It returns `None` and refuses unsupported connected, extended,
merged, spill, sorted, protected, or ambiguous table states without mutation.
`replace_image(target, replacement, *, name=None)` retargets exactly one loaded image while
keeping its drawing and anchor, returns the selected image, and refuses ambiguous targets or shared
relationships. `set_pivot_refresh_on_load(pivots=["Sheet!Pivot"])` returns the resolved cache parts
and scopes refresh metadata to named pivots; pass `all=True` explicitly to select every
relationship-resolved pivot cache. Pivots sharing one cache necessarily share the setting.
Validate or save refuses when recorded value edits would silently stale a preserved local
pivot cache unless that cache is selected for refresh-on-open.

A write to a locked cell on a protected sheet proceeds by default with `ProtectedWriteWarning`;
set `wb.strict_protection = True` to refuse instead. A `PaperRefusal` carries `kind`, `anchor`, and
`options`; invalid types and argument combinations remain ordinary `TypeError` or `ValueError`.
An immediate operation refusal is atomic. Validation/save refusal prevents destination commit, but
prior live in-memory edits still exist.
Path saves use atomic replacement; `HandleRebindWarning` means that commit succeeded but a
caller-owned open handle could not be rebound. Most structured Paper results support `.to_dict()`
with stable `schema` and `version` fields.
