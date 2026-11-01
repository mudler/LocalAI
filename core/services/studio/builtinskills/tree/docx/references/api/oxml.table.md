<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.table`

Custom element classes for tables.

## `CT_Height`

Bases: `BaseOxmlElement`

Used for `w:trHeight` to specify a row height and row height rule.

### `hRule`

```python
hRule: WD_ROW_HEIGHT_RULE | None = OptionalAttribute('w:hRule', WD_ROW_HEIGHT_RULE)
```

### `val`

```python
val: Length | None = OptionalAttribute('w:val', ST_TwipsMeasure)
```

## `CT_Row`

Bases: `BaseOxmlElement`

``<w:tr>`` element.

### `add_tc`

```python
add_tc: Callable[[], CT_Tc]
```

### `get_or_add_trPr`

```python
get_or_add_trPr: Callable[[], CT_TrPr]
```

### `grid_after`

```python
grid_after: int
```

The number of unpopulated layout-grid cells at the end of this row.

### `grid_before`

```python
grid_before: int
```

The number of unpopulated layout-grid cells at the start of this row.

### `tblPrEx`

```python
tblPrEx: CT_TblPrEx | None = ZeroOrOne('w:tblPrEx')
```

### `tc`

```python
tc = ZeroOrMore('w:tc')
```

### `tc_at_grid_offset`

```python
tc_at_grid_offset(grid_offset: int) -> CT_Tc
```

The `tc` element in this tr at exact `grid offset`.

Raises ValueError when this `w:tr` contains no `w:tc` with exact starting `grid_offset`.

### `tc_lst`

```python
tc_lst: list[CT_Tc]
```

### `trHeight_hRule`

```python
trHeight_hRule: WD_ROW_HEIGHT_RULE | None
```

The value of `./w:trPr/w:trHeight/@w:hRule`, or `None` if not present.

### `trHeight_val`

```python
trHeight_val
```

Return the value of `w:trPr/w:trHeight@w:val`, or `None` if not present.

### `trPr`

```python
trPr: CT_TrPr | None = ZeroOrOne('w:trPr')
```

### `tr_idx`

```python
tr_idx: int
```

Index of this `w:tr` element within its parent `w:tbl` element.

## `CT_Tbl`

Bases: `BaseOxmlElement`

``<w:tbl>`` element.

### `add_tr`

```python
add_tr: Callable[[], CT_Row]
```

### `bidiVisual_val`

```python
bidiVisual_val: bool | None
```

Value of `./w:tblPr/w:bidiVisual/@w:val` or `None` if not present.

Controls whether table cells are displayed right-to-left or left-to-right.

### `col_count`

```python
col_count
```

The number of grid columns in this table.

### `iter_tcs`

```python
iter_tcs()
```

Generate each of the `w:tc` elements in this table, left to right and top to
bottom.

Each cell in the first row is generated, followed by each cell in the second
row, etc.

### `new_tbl`

```python
new_tbl(rows: int, cols: int, width: Length) -> CT_Tbl
```

Return a new `w:tbl` element having `rows` rows and `cols` columns.

`width` is distributed evenly between the columns.

### `tblGrid`

```python
tblGrid: CT_TblGrid = OneAndOnlyOne('w:tblGrid')
```

### `tblPr`

```python
tblPr: CT_TblPr = OneAndOnlyOne('w:tblPr')
```

### `tblStyle_val`

```python
tblStyle_val: str | None
```

`w:tblPr/w:tblStyle/@w:val` (a table style id) or `None` if not present.

### `tr`

```python
tr = ZeroOrMore('w:tr')
```

### `tr_lst`

```python
tr_lst: list[CT_Row]
```

## `CT_TblGrid`

Bases: `BaseOxmlElement`

`w:tblGrid` element.

Child of `w:tbl`, holds `w:gridCol> elements that define column count, width, etc.

### `add_gridCol`

```python
add_gridCol: Callable[[], CT_TblGridCol]
```

### `gridCol`

```python
gridCol = ZeroOrMore('w:gridCol', successors=('w:tblGridChange',))
```

### `gridCol_lst`

```python
gridCol_lst: list[CT_TblGridCol]
```

## `CT_TblGridCol`

Bases: `BaseOxmlElement`

`w:gridCol` element, child of `w:tblGrid`, defines a table column.

### `gridCol_idx`

```python
gridCol_idx: int
```

Index of this `w:gridCol` element within its parent `w:tblGrid` element.

### `w`

```python
w: Length | None = OptionalAttribute('w:w', ST_TwipsMeasure)
```

## `CT_TblLayoutType`

Bases: `BaseOxmlElement`

`w:tblLayout` element.

Specifies whether column widths are fixed or can be automatically adjusted based on
content.

### `type`

```python
type: str | None = OptionalAttribute('w:type', ST_TblLayoutType)
```

## `CT_TblPr`

Bases: `BaseOxmlElement`

``<w:tblPr>`` element, child of ``<w:tbl>``, holds child elements that define
table properties such as style and borders.

### `alignment`

```python
alignment: WD_TABLE_ALIGNMENT | None
```

Horizontal alignment of table, `None` if `./w:jc` is not present.

### `autofit`

```python
autofit: bool
```

`False` when there is a `w:tblLayout` child with `@w:type="fixed"`.

Otherwise `True`.

### `bidiVisual`

```python
bidiVisual: CT_OnOff | None = ZeroOrOne('w:bidiVisual', successors=_tag_seq[4:])
```

### `get_or_add_bidiVisual`

```python
get_or_add_bidiVisual: Callable[[], CT_OnOff]
```

### `get_or_add_jc`

```python
get_or_add_jc: Callable[[], CT_Jc]
```

### `get_or_add_tblLayout`

```python
get_or_add_tblLayout: Callable[[], CT_TblLayoutType]
```

### `jc`

```python
jc: CT_Jc | None = ZeroOrOne('w:jc', successors=_tag_seq[8:])
```

### `style`

```python
style
```

Return the value of the ``val`` attribute of the ``<w:tblStyle>`` child or
`None` if not present.

### `tblLayout`

```python
tblLayout: CT_TblLayoutType | None = ZeroOrOne('w:tblLayout', successors=_tag_seq[13:])
```

### `tblStyle`

```python
tblStyle: CT_String | None = ZeroOrOne('w:tblStyle', successors=_tag_seq[1:])
```

## `CT_TblPrEx`

Bases: `BaseOxmlElement`

`w:tblPrEx` element, exceptions to table-properties.

Applied at a lower level, like a `w:tr` to modify the appearance. Possibly used when
two tables are merged. For more see:
http://officeopenxml.com/WPtablePropertyExceptions.php

## `CT_TblWidth`

Bases: `BaseOxmlElement`

Used for `w:tblW` and `w:tcW` and others, specifies a table-related width.

### `type`

```python
type = RequiredAttribute('w:type', ST_TblWidth)
```

### `w`

```python
w: int = RequiredAttribute('w:w', XsdInt)
```

### `width`

```python
width: Length | None
```

EMU length indicated by the combined `w:w` and `w:type` attrs.

## `CT_Tc`

Bases: `BaseOxmlElement`

`w:tc` table cell element.

### `add_p`

```python
add_p: Callable[[], CT_P]
```

### `bottom`

```python
bottom: int
```

The row index that marks the bottom extent of the vertical span of this cell.

This is one greater than the index of the bottom-most row of the span, similar
to how a slice of the cell's rows would be specified.

### `clear_content`

```python
clear_content()
```

Remove all content elements, preserving `w:tcPr` element if present.

Note that this leaves the `w:tc` element in an invalid state because it doesn't
contain at least one block-level element. It's up to the caller to add a
`w:p`child element as the last content element.

### `get_or_add_tcPr`

```python
get_or_add_tcPr: Callable[[], CT_TcPr]
```

### `grid_offset`

```python
grid_offset: int
```

Starting offset of `tc` in the layout-grid columns of its table.

A cell in the leftmost grid-column has offset 0.

### `grid_span`

```python
grid_span: int
```

The integer number of columns this cell spans.

Determined by ./w:tcPr/w:gridSpan/@val, it defaults to 1.

### `inner_content_elements`

```python
inner_content_elements: list[CT_P | CT_Tbl]
```

Generate all `w:p` and `w:tbl` elements in this document-body.

Elements appear in document order. Elements shaded by nesting in a `w:ins` or
other "wrapper" element will not be included.

### `iter_block_items`

```python
iter_block_items()
```

Generate a reference to each of the block-level content elements in this
cell, in the order they appear.

### `left`

```python
left: int
```

The grid column index at which this ``<w:tc>`` element appears.

### `merge`

```python
merge(other_tc: CT_Tc) -> CT_Tc
```

Return top-left `w:tc` element of a new span.

Span is formed by merging the rectangular region defined by using this tc
element and `other_tc` as diagonal corners.

### `new`

```python
new() -> CT_Tc
```

A new `w:tc` element, containing an empty paragraph as the required EG_BlockLevelElt.

### `p`

```python
p = OneOrMore('w:p')
```

### `p_lst`

```python
p_lst: list[CT_P]
```

### `right`

```python
right: int
```

The grid column index that marks the right-side extent of the horizontal span
of this cell.

This is one greater than the index of the right-most column of the span, similar
to how a slice of the cell's columns would be specified.

### `tbl`

```python
tbl = OneOrMore('w:tbl')
```

### `tbl_lst`

```python
tbl_lst: list[CT_Tbl]
```

### `tcPr`

```python
tcPr: CT_TcPr | None = ZeroOrOne('w:tcPr')
```

### `top`

```python
top: int
```

The top-most row index in the vertical span of this cell.

### `vMerge`

```python
vMerge: str | None
```

Value of ./w:tcPr/w:vMerge/@val, `None` if w:vMerge is not present.

### `width`

```python
width: Length | None
```

EMU length represented in `./w:tcPr/w:tcW` or `None` if not present.

## `CT_TcPr`

Bases: `BaseOxmlElement`

``<w:tcPr>`` element, defining table cell properties.

### `get_or_add_gridSpan`

```python
get_or_add_gridSpan: Callable[[], CT_DecimalNumber]
```

### `get_or_add_tcW`

```python
get_or_add_tcW: Callable[[], CT_TblWidth]
```

### `get_or_add_vAlign`

```python
get_or_add_vAlign: Callable[[], CT_VerticalJc]
```

### `gridSpan`

```python
gridSpan: CT_DecimalNumber | None = ZeroOrOne('w:gridSpan', successors=_tag_seq[3:])
```

### `grid_span`

```python
grid_span: int
```

The integer number of columns this cell spans.

Determined by ./w:gridSpan/@val, it defaults to 1.

### `tcW`

```python
tcW: CT_TblWidth | None = ZeroOrOne('w:tcW', successors=_tag_seq[2:])
```

### `vAlign`

```python
vAlign: CT_VerticalJc | None = ZeroOrOne('w:vAlign', successors=_tag_seq[12:])
```

### `vAlign_val`

```python
vAlign_val
```

Value of `w:val` attribute on  `w:vAlign` child.

Value is `None` if `w:vAlign` child is not present. The `w:val` attribute on
`w:vAlign` is required.

### `vMerge`

```python
vMerge: CT_VMerge | None = ZeroOrOne('w:vMerge', successors=_tag_seq[5:])
```

### `vMerge_val`

```python
vMerge_val
```

The value of the ./w:vMerge/@val attribute, or `None` if the w:vMerge element
is not present.

### `width`

```python
width: Length | None
```

EMU length in `./w:tcW` or `None` if not present or its type is not 'dxa'.

## `CT_TrPr`

Bases: `BaseOxmlElement`

``<w:trPr>`` element, defining table row properties.

### `get_or_add_trHeight`

```python
get_or_add_trHeight: Callable[[], CT_Height]
```

### `gridAfter`

```python
gridAfter: CT_DecimalNumber | None = ZeroOrOne('w:gridAfter', successors=_tag_seq[4:])
```

### `gridBefore`

```python
gridBefore: CT_DecimalNumber | None = ZeroOrOne('w:gridBefore', successors=_tag_seq[3:])
```

### `grid_after`

```python
grid_after: int
```

The number of unpopulated layout-grid cells at the end of this row.

### `grid_before`

```python
grid_before: int
```

The number of unpopulated layout-grid cells at the start of this row.

### `trHeight`

```python
trHeight: CT_Height | None = ZeroOrOne('w:trHeight', successors=_tag_seq[8:])
```

### `trHeight_hRule`

```python
trHeight_hRule: WD_ROW_HEIGHT_RULE | None
```

Return the value of `w:trHeight@w:hRule`, or `None` if not present.

### `trHeight_val`

```python
trHeight_val
```

Return the value of `w:trHeight@w:val`, or `None` if not present.

## `CT_VMerge`

Bases: `BaseOxmlElement`

``<w:vMerge>`` element, specifying vertical merging behavior of a cell.

### `val`

```python
val: str | None = OptionalAttribute('w:val', ST_Merge, default=ST_Merge.CONTINUE)
```

## `CT_VerticalJc`

Bases: `BaseOxmlElement`

`w:vAlign` element, specifying vertical alignment of cell.

### `val`

```python
val: WD_CELL_VERTICAL_ALIGNMENT = RequiredAttribute('w:val', WD_CELL_VERTICAL_ALIGNMENT)
```
