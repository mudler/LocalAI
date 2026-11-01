<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.rebind`

Layout rebind - the template-migration primitive (paper-pptx addition).

## `RebindReport`

```python
RebindReport(source_layout: str, source_layout_name: str, target_layout: str, target_layout_name: str, placeholder_map_used: Tuple[Tuple[int, Optional[int]], ...], baked_orphans: Tuple[str, ...], run_shifts: Tuple[RunShift, ...]) -> None
```

What one rebind did: the mapping used, orphan handling, and every resolution shift.

Fields:

* ``source_layout`` / ``source_layout_name`` -- partname and display name of the
  layout the slide was bound to before.
* ``target_layout`` / ``target_layout_name`` -- partname and display name of the
  layout the slide is bound to after.
* ``placeholder_map_used`` -- the resolved ``(source_idx, target_idx)`` pairs; a
  ``target_idx`` of ``None`` marks a source placeholder that was orphaned.
* ``baked_orphans`` -- names of orphan placeholders converted to free shapes (only
  populated under ``orphan_policy="bake"``).
* ``run_shifts`` -- one `RunShift` for every run whose resolved effective
  values changed; empty when the rebind preserved appearance exactly.

### `baked_orphans`

```python
baked_orphans: Tuple[str, ...]
```

### `placeholder_map_used`

```python
placeholder_map_used: Tuple[Tuple[int, Optional[int]], ...]
```

### `run_shifts`

```python
run_shifts: Tuple[RunShift, ...]
```

### `source_layout`

```python
source_layout: str
```

### `source_layout_name`

```python
source_layout_name: str
```

### `target_layout`

```python
target_layout: str
```

### `target_layout_name`

```python
target_layout_name: str
```

### `to_dict`

```python
to_dict() -> dict
```

Return the rebind report as a JSON-ready dict stamped with its schema and version.

## `RunShift`

```python
RunShift(part: str, shape_id: int, block_ordinal: int, run_index: int, text: str, before: dict, after: dict) -> None
```

One run whose resolved effective values changed across the rebind.

Runs are identified by (shape_id, block_ordinal-within-shape, run_index) - a STABLE
key that survives shapes being added or removed elsewhere on the slide. Keying by
the slide-global block index would pair unrelated runs the moment any earlier shape
disappears.

Fields:

* ``part`` -- partname of the slide the run lives on.
* ``shape_id`` -- id of the shape containing the run.
* ``block_ordinal`` -- 0-based paragraph-block index within that shape.
* ``run_index`` -- 0-based run index within the block.
* ``text`` -- the run's text (for human legibility of the shift).
* ``before`` / ``after`` -- the run's resolved effective-font payload
  (``EffectiveFont.to_dict()``) before and after the rebind.

### `after`

```python
after: dict
```

### `before`

```python
before: dict
```

### `block_ordinal`

```python
block_ordinal: int
```

### `part`

```python
part: str
```

### `run_index`

```python
run_index: int
```

### `shape_id`

```python
shape_id: int
```

### `text`

```python
text: str
```

### `to_dict`

```python
to_dict() -> dict
```

Return this run change as a JSON-ready dict.

## `SCHEMA_NAME`

```python
SCHEMA_NAME = 'paper-rebind-report'
```

## `SCHEMA_VERSION`

```python
SCHEMA_VERSION = 1
```

## `rebind_layout`

```python
rebind_layout(slide: 'Slide', target_layout: 'SlideLayout', *, placeholder_map: 'SlideLayout' = 'auto', orphan_policy: str = 'refuse') -> RebindReport
```

Rebind `slide` to `target_layout`; return the required shift report.
