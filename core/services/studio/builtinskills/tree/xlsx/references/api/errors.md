<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.errors`

Typed exceptions for paper-xlsx safety refusals.

## `AmbiguousTargetError`

Bases: `PaperRefusal`

The addressed target matches more than one candidate.

## `BoundaryViolationError`

Bases: `PaperRefusal`

The operation would cross a declared boundary (range, sheet, or
package region) it is not allowed to cross.

## `HandleRebindWarning`

Bases: `UserWarning`

A path-backed save committed correctly, but the caller's open file
handle could not be rebound to the replacement file.

## `OracleTimeoutError`

Bases: `PaperRefusal`

The LibreOffice oracle did not finish within the allowed time.

## `OracleUnavailableError`

Bases: `PaperRefusal`

No LibreOffice installation could be found to act as the oracle.

## `PaperRefusal`

```python
PaperRefusal(*args, kind = None, anchor = None, options = None)
```

Bases: `Exception`

Base class for all safe refusals.

Refusals are atomic: when one is raised, the workbook model, the dirty
ledger, and every file on disk are exactly as they were before the
refused operation began.

Structured fields (populated progressively — message
text is always the source of truth):

- ``kind``: stable machine-readable string ("ambiguous-label", ...)
- ``anchor``: sheet-qualified address or part name the refusal is
  about, or None
- ``options``: suggested remedies / candidate addresses (list)

### `anchor`

```python
anchor = anchor
```

### `kind`

```python
kind = kind
```

### `options`

```python
options = list(options) if options else []
```

## `ProtectedWriteWarning`

Bases: `UserWarning`

A write landed on a locked cell of a protected sheet. The write
proceeds — openpyxl-level protection is advisory, and this library
reports it rather than enforcing or bypassing it — but
the human who protected the sheet expected the cell to be read-only.
Set ``wb.strict_protection = True`` to turn these writes into typed
refusals.

## `RelationshipPolicyError`

Bases: `PaperRefusal`

The operation would rewrite or renumber package relationships in a
way that could detach preserved content.

## `TargetNotFoundError`

Bases: `PaperRefusal`

The addressed target does not exist in the workbook or package.

## `UnsupportedStructureError`

Bases: `PaperRefusal`

The operation would require understanding or rewriting structure this
library cannot handle safely; performing it would risk silent damage.
