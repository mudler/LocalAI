<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.pivots`

Resolve pivot names and patch only cache refresh metadata.

## `parts_referencing_sheet`

```python
parts_referencing_sheet(wb, sheet_title)
```

Return local pivot-cache parts that may depend on one worksheet.

## `plan_refresh`

```python
plan_refresh(zin, parts, plan)
```

Add pivot refresh requests to a package byte plan.

**Parameters:**

- **zin** (`ZipFile`) – Open workbook package.
- **parts** (`iterable of str`) – Pivot cache-definition part names to update.
- **plan** (`dict`) – Planned replacement bytes keyed by part name.

**Returns:**

- `list of str` – Part names whose refresh metadata changed.

## `resolve_requests`

```python
resolve_requests(wb, pivots = None, *, all = False)
```

Resolve requested pivots to their cache-definition parts.

**Parameters:**

- **wb** (`Workbook`) – Preserve-mode workbook containing the pivots.
- **pivots** (`iterable of str | None`) – Pivot names, optionally qualified by worksheet name.
- **all** (`bool`) – Select every loaded pivot cache instead of named pivots.

**Returns:**

- `list of str` – Sorted package part names for the selected pivot caches.

## `snapshot_sources`

```python
snapshot_sources(wb)
```

Capture semantic local-source bindings when preservation arms.

## `source_impacts`

```python
source_impacts(wb, ledger, *, force_recalculation = False)
```

Return local pivot caches that workbook edits can make stale.

## `validate_source_freshness`

```python
validate_source_freshness(wb, ledger)
```

Refuse silent stale pivot caches without explicit refresh consent.
