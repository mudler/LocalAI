<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.oracle`

Bounded recalculation and certification via headless LibreOffice.

**Not available here: LibreOffice is not installed. Do not call anything on this page.**

## `CertificationResult`

```python
CertificationResult(status, checked, divergences, volatile_excluded, unverifiable, external_excluded = None, unsupported_excluded = None, input_excluded = None, artifact_sha256 = None)
```

### `BASELINE_UNVERIFIABLE`

```python
BASELINE_UNVERIFIABLE = 'BASELINE_UNVERIFIABLE'
```

### `CERTIFIED`

```python
CERTIFIED = 'CERTIFIED'
```

### `DIVERGED`

```python
DIVERGED = 'DIVERGED'
```

### `SCHEMA`

```python
SCHEMA = 'oracle_certification'
```

### `VERSION`

```python
VERSION = 1
```

### `artifact_sha256`

```python
artifact_sha256 = artifact_sha256
```

### `checked`

```python
checked = checked
```

### `classify_tolerance`

```python
classify_tolerance(*, abs_tol = None, rel_tol = None)
```

Classify recorded strict divergences under a numeric tolerance.

Strict matches are not re-evaluated, so this cannot impose a policy
narrower than Paper's strict comparator. The classification does not
alter `status` or establish complete coverage. Formula errors,
nonnumeric mismatches, and non-finite values always remain outside.

**Returns:**

- `openpyxl.oracle.NumericalToleranceResult` – A measurement-only classification of the recorded strict
divergences.

### `divergences`

```python
divergences = divergences
```

### `external_excluded`

```python
external_excluded = external_excluded or []
```

### `input_excluded`

```python
input_excluded = input_excluded or []
```

### `status`

```python
status = status
```

### `to_dict`

```python
to_dict()
```

### `unsupported_excluded`

```python
unsupported_excluded = unsupported_excluded or []
```

### `unverifiable`

```python
unverifiable = unverifiable
```

### `volatile_excluded`

```python
volatile_excluded = volatile_excluded
```

## `DATE_SERIAL_ABS_FLOOR`

```python
DATE_SERIAL_ABS_FLOOR = 1e-11
```

## `ERROR_TOKENS`

```python
ERROR_TOKENS = frozenset(EXCEL_ERROR_CODES)
```

## `Evaluation`

```python
Evaluation(inputs, outputs, errors, certification, artifact_sha256 = None)
```

One what-if run: inputs applied to a TEMP COPY through the spine,
LibreOffice recalculated, outputs harvested. Pinned surface.

### `FORMULA_ERRORS_DETECTED`

```python
FORMULA_ERRORS_DETECTED = 'FORMULA_ERRORS_DETECTED'
```

### `NO_DETECTED_FORMULA_ERRORS`

```python
NO_DETECTED_FORMULA_ERRORS = 'NO_DETECTED_FORMULA_ERRORS'
```

### `SCHEMA`

```python
SCHEMA = 'evaluation'
```

### `VERSION`

```python
VERSION = 2
```

### `artifact_sha256`

```python
artifact_sha256 = artifact_sha256
```

### `certification`

```python
certification = certification
```

### `error_cells`

```python
error_cells
```

### `errors`

```python
errors = errors
```

### `inputs`

```python
inputs = inputs
```

### `outputs`

```python
outputs = outputs
```

### `status`

```python
status
```

### `to_dict`

```python
to_dict()
```

## `NUMERIC_ULPS`

```python
NUMERIC_ULPS = 4
```

## `NumericalToleranceResult`

```python
NumericalToleranceResult(strict_status, coverage_complete, absolute_tolerance, relative_tolerance, within_tolerance, outside_tolerance)
```

Measurement-only classification of strict numeric divergences.

`strict_status` remains the original certification result. Coverage and
tolerance buckets are reported separately; this object never replaces or
weakens strict certification.

### `SCHEMA`

```python
SCHEMA = 'oracle_numerical_tolerance'
```

### `VERSION`

```python
VERSION = 1
```

### `absolute_tolerance`

```python
absolute_tolerance = absolute_tolerance
```

### `all_divergences_within_tolerance`

```python
all_divergences_within_tolerance = None if not within_tolerance and not outside_tolerance else not outside_tolerance
```

### `coverage_complete`

```python
coverage_complete = coverage_complete
```

### `outside_tolerance`

```python
outside_tolerance = outside_tolerance
```

### `relative_tolerance`

```python
relative_tolerance = relative_tolerance
```

### `strict_status`

```python
strict_status = strict_status
```

### `to_dict`

```python
to_dict()
```

### `within_tolerance`

```python
within_tolerance = within_tolerance
```

## `ORACLE_UNSUPPORTED_FUNCS`

```python
ORACLE_UNSUPPORTED_FUNCS = frozenset(['LAMBDA', 'LET', 'MAP', 'REDUCE', 'SCAN', 'BYROW', 'BYCOL', 'MAKEARRAY', 'ISOMITTED', 'STOCKHISTORY', 'RTD', 'WEBSERVICE', 'FILTERXML', 'IMAGE', 'PY', 'CUBEVALUE', 'CUBEMEMBER', 'CUBESET', 'CUBESETCOUNT', 'CUBERANKEDMEMBER', 'CUBEMEMBERPROPERTY', 'CUBEKPIMEMBER'])
```

## `RecalcResult`

```python
RecalcResult(cells_scanned, formula_cells, errors, *, output_kind = None, written = None, verified_unchanged = None, excluded = None, package_diff = None, pivot_refreshes = None, artifact_sha256 = None, calculation_artifact_sha256 = None)
```

### `FORMULA_ERRORS_DETECTED`

```python
FORMULA_ERRORS_DETECTED = 'FORMULA_ERRORS_DETECTED'
```

### `NO_DETECTED_FORMULA_ERRORS`

```python
NO_DETECTED_FORMULA_ERRORS = 'NO_DETECTED_FORMULA_ERRORS'
```

### `SCHEMA`

```python
SCHEMA = 'oracle_recalc'
```

### `VERSION`

```python
VERSION = 2
```

### `artifact_sha256`

```python
artifact_sha256 = artifact_sha256
```

### `calculation_artifact_sha256`

```python
calculation_artifact_sha256 = calculation_artifact_sha256
```

### `cells_scanned`

```python
cells_scanned = cells_scanned
```

### `cells_written`

```python
cells_written
```

Return the number of formula caches written to the candidate.

**Returns:**

- `int` – Number of entries in `written`.

### `engine`

```python
engine = 'libreoffice'
```

### `errors`

```python
errors = errors
```

### `excluded`

```python
excluded = dict(excluded or {})
```

### `formula_cells`

```python
formula_cells = formula_cells
```

### `output_kind`

```python
output_kind = output_kind
```

### `package_diff`

```python
package_diff = list(package_diff or ())
```

### `pivot_refreshes`

```python
pivot_refreshes = list(pivot_refreshes or ())
```

### `status`

```python
status
```

### `to_dict`

```python
to_dict()
```

### `verified_unchanged`

```python
verified_unchanged = list(verified_unchanged or ())
```

### `written`

```python
written = list(written or ())
```

## `available`

```python
available()
```

True when a LibreOffice installation can be found.

## `certify`

```python
certify(source, *, timeout = 120.0)
```

The divergence check: does LibreOffice reproduce the file's own
cached values? Pre-flights on an untouched temp copy; the caller's file
is never modified. Returns measurements, never judgments.

## `evaluate`

```python
evaluate(source, set, read, *, timeout = 120.0)
```

Scenario run against ``source`` (path/bytes/file-like): apply
``set`` inputs to a temp copy through the preserve spine, recalculate
with LibreOffice, harvest ``read`` outputs. The source and every
caller file stay untouched. One LibreOffice run serves both the
outputs and the certification (original caches vs computed, with
inputs' downstream cells excluded as ``input_excluded``).

## `evaluate_many`

```python
evaluate_many(source, cases, read, *, pool_size = 2, timeout = 120.0)
```

``evaluate`` for a list of input dicts, sharing warm LibreOffice
profiles across cases (the pool is an implementation
detail — ``pool_size`` per-thread-isolated profiles, created lazily,
crash-replaced once, destroyed before return).

## `find_soffice`

```python
find_soffice()
```

Locate the LibreOffice binary, or None.

## `recalc`

```python
recalc(source, *, output_path = None, timeout = 120.0)
```

Recalculate a temporary copy with LibreOffice.

With no ``output_path``, return error-scan evidence and write nothing.
With a separate ``output_path``, build a Paper-preserved candidate by
splicing eligible calculated caches into the original package structure.
If those writes or the requested full recalculation can affect a local
pivot source, the candidate requests refresh-on-open and
``RecalcResult.pivot_refreshes`` reports the Excel refresh requirement.
LibreOffice's rewritten package is never delivered, and the result makes
no claim of Excel equivalence or financial correctness.
