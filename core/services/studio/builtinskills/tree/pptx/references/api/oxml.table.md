<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.table`

Custom element classes for table-related XML elements

## `CT_Table`

Bases: `BaseOxmlElement`

`a:tbl` custom element class

### `add_tr`

```python
add_tr(height: Length) -> CT_TableRow
```

Return a newly created `a:tr` child element having its `h` attribute set to `height`.

### `bandCol`

```python
bandCol: bool
```

### `bandRow`

```python
bandRow: bool
```

### `firstCol`

```python
firstCol: bool
```

### `firstRow`

```python
firstRow: bool
```

### `get_or_add_tblPr`

```python
get_or_add_tblPr: Callable[[], CT_TableProperties]
```

### `insert_tr_at`

```python
insert_tr_at(idx: int, height: Length) -> CT_TableRow
```

Return a new `a:tr` inserted so it becomes the row at `idx` (paper-pptx addition).

### `iter_tcs`

```python
iter_tcs() -> Iterator[CT_TableCell]
```

Generate each `a:tc` element in this tbl.

`a:tc` elements are generated left-to-right, top-to-bottom.

### `lastCol`

```python
lastCol: bool
```

### `lastRow`

```python
lastRow: bool
```

### `new_tbl`

```python
new_tbl(rows: int, cols: int, width: int, height: int, tableStyleId: str | None = None) -> CT_Table
```

Return a new `p:tbl` element tree.

### `tblGrid`

```python
tblGrid: CT_TableGrid = OneAndOnlyOne('a:tblGrid')
```

### `tblPr`

```python
tblPr: CT_TableProperties | None = ZeroOrOne('a:tblPr', successors=_tag_seq[1:])
```

### `tc`

```python
tc(row_idx: int, col_idx: int) -> CT_TableCell
```

Return `a:tc` element at `row_idx`, `col_idx`.

### `tr`

```python
tr = ZeroOrMore('a:tr', successors=_tag_seq[3:])
```

### `tr_lst`

```python
tr_lst: list[CT_TableRow]
```

## `CT_TableCell`

Bases: `BaseOxmlElement`

`a:tc` custom element class

### `anchor`

```python
anchor: MSO_VERTICAL_ANCHOR | None
```

String held in `anchor` attribute of `a:tcPr` child element of this `a:tc` element.

### `append_ps_from`

```python
append_ps_from(spanned_tc: CT_TableCell)
```

Append `a:p` elements taken from `spanned_tc`.

Any non-empty paragraph elements in `spanned_tc` are removed and appended to the
text-frame of this cell. If `spanned_tc` is left with no content after this process, a
single empty `a:p` element is added to ensure the cell is compliant with the spec.

### `col_idx`

```python
col_idx: int
```

Offset of this cell's column in its table.

### `get_or_add_tcPr`

```python
get_or_add_tcPr: Callable[[], CT_TableCellProperties]
```

### `get_or_add_txBody`

```python
get_or_add_txBody: Callable[[], CT_TextBody]
```

### `gridSpan`

```python
gridSpan: int = OptionalAttribute('gridSpan', XsdInt, default=1)
```

### `hMerge`

```python
hMerge: bool = OptionalAttribute('hMerge', XsdBoolean, default=False)
```

### `is_merge_origin`

```python
is_merge_origin: bool
```

True if cell is top-left in merged cell range.

### `is_spanned`

```python
is_spanned: bool
```

True if cell is in merged cell range but not merge origin cell.

### `marB`

```python
marB: Length
```

Bottom margin value represented in `marB` attribute.

### `marL`

```python
marL: Length
```

Left margin value represented in `marL` attribute.

### `marR`

```python
marR: Length
```

Right margin value represented in `marR` attribute.

### `marT`

```python
marT: Length
```

Top margin for this cell.

This value is stored in the `marT` attribute of the `a:tcPr` child element of this `a:tc`.

Read/write. If the attribute is not present, the default value `45720` (0.05 inches) is
returned for top and bottom; `91440` (0.10 inches) is the default for left and right.
Assigning `None` to any `marX` property clears that attribute from the element,
effectively setting it to the default value.

### `new`

```python
new() -> CT_TableCell
```

Return a new `a:tc` element subtree.

### `rowSpan`

```python
rowSpan: int = OptionalAttribute('rowSpan', XsdInt, default=1)
```

### `row_idx`

```python
row_idx: int
```

Offset of this cell's row in its table.

### `tbl`

```python
tbl: CT_Table
```

Table element this cell belongs to.

### `tcPr`

```python
tcPr: CT_TableCellProperties | None = ZeroOrOne('a:tcPr', successors=_tag_seq[2:])
```

### `text`

```python
text: str
```

str text contained in cell

### `txBody`

```python
txBody: CT_TextBody | None = ZeroOrOne('a:txBody', successors=_tag_seq[1:])
```

### `vMerge`

```python
vMerge: bool = OptionalAttribute('vMerge', XsdBoolean, default=False)
```

## `CT_TableCellProperties`

Bases: `BaseOxmlElement`

`a:tcPr` custom element class

### `anchor`

```python
anchor: MSO_VERTICAL_ANCHOR | None = OptionalAttribute('anchor', MSO_VERTICAL_ANCHOR)
```

### `eg_fillProperties`

```python
eg_fillProperties = ZeroOrOneChoice((Choice('a:noFill'), Choice('a:solidFill'), Choice('a:gradFill'), Choice('a:blipFill'), Choice('a:pattFill'), Choice('a:grpFill')), successors=('a:headers', 'a:extLst'))
```

### `marB`

```python
marB: Length | None = OptionalAttribute('marB', ST_Coordinate32)
```

### `marL`

```python
marL: Length | None = OptionalAttribute('marL', ST_Coordinate32)
```

### `marR`

```python
marR: Length | None = OptionalAttribute('marR', ST_Coordinate32)
```

### `marT`

```python
marT: Length | None = OptionalAttribute('marT', ST_Coordinate32)
```

## `CT_TableCol`

Bases: `BaseOxmlElement`

`a:gridCol` custom element class.

### `w`

```python
w: Length = RequiredAttribute('w', ST_Coordinate)
```

## `CT_TableGrid`

Bases: `BaseOxmlElement`

`a:tblGrid` custom element class.

### `add_gridCol`

```python
add_gridCol(width: Length) -> CT_TableCol
```

A newly appended `a:gridCol` child element having its `w` attribute set to `width`.

### `gridCol`

```python
gridCol = ZeroOrMore('a:gridCol')
```

### `gridCol_lst`

```python
gridCol_lst: list[CT_TableCol]
```

### `insert_gridCol_at`

```python
insert_gridCol_at(idx: int, width: Length) -> CT_TableCol
```

Return a new `a:gridCol` inserted so it becomes the column at `idx`.

paper-pptx addition. Insertion is relative to the existing `a:gridCol` siblings, so
a trailing `a:extLst` (if any) stays last per the schema's child sequence.

## `CT_TableProperties`

Bases: `BaseOxmlElement`

`a:tblPr` custom element class.

### `bandCol`

```python
bandCol = OptionalAttribute('bandCol', XsdBoolean, default=False)
```

### `bandRow`

```python
bandRow = OptionalAttribute('bandRow', XsdBoolean, default=False)
```

### `firstCol`

```python
firstCol = OptionalAttribute('firstCol', XsdBoolean, default=False)
```

### `firstRow`

```python
firstRow = OptionalAttribute('firstRow', XsdBoolean, default=False)
```

### `lastCol`

```python
lastCol = OptionalAttribute('lastCol', XsdBoolean, default=False)
```

### `lastRow`

```python
lastRow = OptionalAttribute('lastRow', XsdBoolean, default=False)
```

## `CT_TableRow`

Bases: `BaseOxmlElement`

`a:tr` custom element class.

### `add_tc`

```python
add_tc() -> CT_TableCell
```

A newly added minimal valid `a:tc` child element.

### `h`

```python
h: Length = RequiredAttribute('h', ST_Coordinate)
```

### `insert_tc_at`

```python
insert_tc_at(idx: int) -> CT_TableCell
```

Return a new minimal `a:tc` inserted so it becomes the cell at `idx`.

paper-pptx addition. Insertion is relative to the existing `a:tc` siblings, so a
trailing `a:extLst` (if any) stays last per the schema's child sequence.

### `row_idx`

```python
row_idx: int
```

Offset of this row in its table.

### `tc`

```python
tc = ZeroOrMore('a:tc', successors=('a:extLst',))
```

### `tc_lst`

```python
tc_lst: list[CT_TableCell]
```

## `TcRange`

```python
TcRange(tc: CT_TableCell, other_tc: CT_TableCell)
```

Bases: `object`

A 2D block of `a:tc` cell elements in a table.

This object assumes the structure of the underlying table does not change during its lifetime.
Structural changes in this context would be insertion or removal of rows or columns.

The client is expected to create, use, and then abandon an instance in the context of a single
user operation that is known to have no structural side-effects of this type.

### `contains_merged_cell`

```python
contains_merged_cell() -> bool
```

True if one or more cells in range are part of a merged cell.

### `dimensions`

```python
dimensions() -> tuple[int, int]
```

(row_count, col_count) pair describing size of range.

### `from_merge_origin`

```python
from_merge_origin(tc: CT_TableCell)
```

Return instance created from merge-origin tc element.

### `in_same_table`

```python
in_same_table()
```

True if both cells provided to constructor are in same table.

### `iter_except_left_col_tcs`

```python
iter_except_left_col_tcs()
```

Generate each `a:tc` element not in leftmost column of range.

### `iter_except_top_row_tcs`

```python
iter_except_top_row_tcs()
```

Generate each `a:tc` element in non-first rows of range.

### `iter_left_col_tcs`

```python
iter_left_col_tcs()
```

Generate each `a:tc` element in leftmost column of range.

### `iter_tcs`

```python
iter_tcs()
```

Generate each `a:tc` element in this range.

Cell elements are generated left-to-right, top-to-bottom.

### `iter_top_row_tcs`

```python
iter_top_row_tcs()
```

Generate each `a:tc` element in topmost row of range.

### `move_content_to_origin`

```python
move_content_to_origin()
```

Move all paragraphs in range to origin cell.
