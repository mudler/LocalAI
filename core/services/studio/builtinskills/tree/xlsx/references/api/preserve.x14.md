<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.x14`

Conditional-formatting edits on sheets whose rules carry x14 twins.

## `check_dv_coexistence`

```python
check_dv_coexistence(ws, scan, original)
```

Refuse only when a classic DV range intersects an x14 DV range —
otherwise classic edits proceed and the x14 block stays verbatim.

## `plan_cf_composed`

```python
plan_cf_composed(wb, ws, scan, original, armed_blocks)
```

(classic_cf_replacement_bytes, extlst_replacement_or_None).

``armed_blocks``: the arm-time model renders (regions._render_cf
tuple), positionally corresponding to the original document's classic
CF elements.

## `sheet_has_cf_twins`

```python
sheet_has_cf_twins(scan, original)
```

True when classic rules carry twin pointers or the extLst carries
x14 conditional formattings.
