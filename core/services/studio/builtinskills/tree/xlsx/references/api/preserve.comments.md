<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.comments`

Comments on sheets whose original package carries NO comment machinery —

## `COMMENTS_CONTENT_TYPE`

```python
COMMENTS_CONTENT_TYPE = 'application/vnd.openxmlformats-officedocument.spreadsheetml.comments+xml'
```

## `VML_CONTENT_TYPE`

```python
VML_CONTENT_TYPE = 'application/vnd.openxmlformats-officedocument.vmlDrawing'
```

## `comment_anchors_precede_shift`

```python
comment_anchors_precede_shift(zin, sheet_part, names, axis, index)
```

Whether every source note anchor is wholly before one shift.

## `comment_machinery_kind`

```python
comment_machinery_kind(zin, sheet_part, names)
```

Classify a sheet's comment and VML relationships.

**Parameters:**

- **zin** (`ZipFile`) – Open workbook package.
- **sheet_part** (`str`) – Worksheet part name in the package.
- **names** (`set[str]`) – Part names present in the package.

**Returns:**

- `str | None` – ``"comments"``, ``"other-vml"``, or ``None``.

## `plan_comment_creation`

```python
plan_comment_creation(wb, ws, sheet_part, zin, part_plan, names)
```

Create the comments + VML parts for one comment-free sheet; returns
the crafted <legacyDrawing r:id> bytes for the region splice.

## `sheet_has_comment_machinery`

```python
sheet_has_comment_machinery(zin, sheet_part, names)
```

True only for comment relationships or VML note shapes.
