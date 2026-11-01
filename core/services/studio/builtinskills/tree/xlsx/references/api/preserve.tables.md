<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.tables`

Loaded-table mutation, and the table row discipline.

## `TABLE_CONTENT_TYPE`

```python
TABLE_CONTENT_TYPE = 'application/vnd.openxmlformats-officedocument.spreadsheetml.table+xml'
```

## `append_table_row`

```python
append_table_row(ws, table_name, values)
```

Atomically append one row to a supported named worksheet table.

The operation plans values, calculated columns, styles, totals movement,
protection, table geometry, and retained OOXML before changing the model.
Unsupported connected, extended, merged, spill, sorted, or ambiguous table
states raise a typed refusal and leave the workbook unchanged.

**Parameters:**

- **ws** (`Worksheet`) – Worksheet containing the table.
- **table_name** (`str`) – Name of the table to expand.
- **values** (`iterable | mapping`) – Row values as a sequence or column-name mapping.

**Returns:**

- `None` – ``None``.

## `plan_table_lifecycle`

```python
plan_table_lifecycle(wb, ws, sheet_part, zin, armed_names, plan, part_plan, names)
```

Plan table ADD/REMOVE for one sheet: parts via the engine, the
sheet's tableParts element rebuilt as crafted bytes (returned; the
caller rides them through the region splice). ``armed_names`` is the
arm-time tuple of table names.

## `plan_table_mutations`

```python
plan_table_mutations(wb, ws, sheet_part, zin, changed_names, plan, armed_tables = None)
```

Patch each changed loaded table into its ORIGINAL part.

## `sheet_table_parts`

```python
sheet_table_parts(zin, sheet_part)
```

`{displayName: (part_name, original_bytes)}` for one sheet, resolved
through the ORIGINAL rels (producers number table parts arbitrarily —
Table.path's id-derived guess is not trustworthy).

## `validate_table`

```python
validate_table(tbl, original_ref)
```

Geometry guards, against the ORIGINAL ref.
