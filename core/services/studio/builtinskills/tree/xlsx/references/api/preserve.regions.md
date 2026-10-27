<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.regions`

Fully-modeled worksheet satellite elements: faithful serialization,

## `CT_ORDER_INDEX`

```python
CT_ORDER_INDEX = {tag: i for i, tag in enumerate(CT_WORKSHEET_ORDER)}
```

## `CT_WORKSHEET_ORDER`

```python
CT_WORKSHEET_ORDER = ['sheetPr', 'dimension', 'sheetViews', 'sheetFormatPr', 'cols', 'sheetData', 'sheetCalcPr', 'sheetProtection', 'protectedRanges', 'scenarios', 'autoFilter', 'sortState', 'dataConsolidate', 'customSheetViews', 'mergeCells', 'phoneticPr', 'conditionalFormatting', 'dataValidations', 'hyperlinks', 'printOptions', 'pageMargins', 'pageSetup', 'headerFooter', 'rowBreaks', 'colBreaks', 'customProperties', 'cellWatches', 'ignoredErrors', 'smartTags', 'drawing', 'legacyDrawing', 'legacyDrawingHF', 'picture', 'oleObjects', 'controls', 'webPublishItems', 'tableParts', 'extLst']
```

## `DETECT_ONLY_REGIONS`

```python
DETECT_ONLY_REGIONS = []
```

## `REGION_BY_TAG`

```python
REGION_BY_TAG = {r.tag: r for r in SPLICEABLE_REGIONS}
```

## `Region`

```python
Region(tag, serialize)
```

### `render`

```python
render(ws)
```

### `serialize`

```python
serialize = serialize
```

### `tag`

```python
tag = tag
```

## `SAVER_CRAFTED_REGIONS`

```python
SAVER_CRAFTED_REGIONS = frozenset(['tableParts', 'legacyDrawing', 'extLst', 'drawing'])
```

## `SPLICEABLE_REGIONS`

```python
SPLICEABLE_REGIONS = [Region('sheetPr', _sheet_pr), Region('sheetViews', _views), Region('sheetFormatPr', _sheet_format), Region('cols', _cols), Region('sheetProtection', _protection), Region('scenarios', _scenarios), Region('autoFilter', _auto_filter), Region('mergeCells', _merged), Region('dataValidations', _validations), Region('printOptions', _print_options), Region('pageMargins', _margins), Region('pageSetup', _page_setup), Region('headerFooter', _header), Region('rowBreaks', _row_breaks), Region('colBreaks', _col_breaks)]
```

## `diff_regions`

```python
diff_regions(ws, armed_snapshot)
```

Return `{tag: new_serialization}` for regions the user changed.

Rendered twice, second pass kept: the arm snapshot is the settled
render (ledger double-render), so the comparison must
be settled-vs-settled or an impure serializer's first-pass output
false-dirties the region.

## `diff_row_attrs`

```python
diff_row_attrs(ws, armed_snapshot)
```

Return `{row_index: {attr: value}`} for changed rows; a row present in
the arm snapshot but now attribute-free maps to an empty dict.

## `hyperlink_signatures`

```python
hyperlink_signatures(ws)
```

Per-cell hyperlink signatures, excluding the relationship id (ids for
new links are allocated at save time and must not affect detection).

## `render_cf_for_write`

```python
render_cf_for_write(ws)
```

Serialize conditional formatting FOR WRITING, mirroring the stock
writer's dxf handling (worksheet/_writer.py write_formatting): rules
carrying a dxf get a dxfId allocated in the workbook's differential
styles; the new dxfs are appended to styles.xml by the styles planner.

## `render_hyperlinks_for_write`

```python
render_hyperlinks_for_write(ws)
```

The hyperlinks element from the cells' link objects (ids must already
be assigned for external links).

## `snapshot_regions`

```python
snapshot_regions(ws)
```

Serialize every tracked region of one worksheet (arm time / save
time; comparing the two detects user changes).

## `snapshot_row_attrs`

```python
snapshot_row_attrs(ws)
```

Row display attributes (they serialize as attributes of <row>
elements inside sheetData, not as a separate element).
