<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.compose`

Cross-presentation slide import and deck merge (paper-pptx addition).

## `ImportReport`

```python
ImportReport(mode: str, source_slide: str, dest_slide: str, dest_slide_id: int, position: int, layout_binding: str, layout_binding_method: str, placeholder_map_used: Tuple[Tuple[int, Optional[int]], ...], parts_added: Tuple[str, ...], parts_reused: Tuple[str, ...], notes_copied: bool, comments_dropped: int, section: Optional[str], section_id: Optional[str], baked_shapes: Tuple[str, ...], dropped_placeholders: Tuple[str, ...], run_shifts: tuple) -> None
```

What one import did. Deterministic; `.to_dict()` is goldenable.

Fields:

* ``mode`` -- the reconciliation mode used ("adopt_theme", "keep_appearance", "bake").
* ``source_slide`` -- partname of the imported slide in the source presentation.
* ``dest_slide`` / ``dest_slide_id`` -- partname and permanent slide id of the new
  slide in the destination.
* ``position`` -- 0-based index the new slide was inserted at.
* ``layout_binding`` -- partname of the destination layout the slide is bound to.
* ``layout_binding_method`` -- how that layout was chosen ("name-match",
  "type-match", "explicit", "transplant", or "blank-fallback"). Automatic
  methods always identify a unique candidate at their matching tier.
* ``placeholder_map_used`` -- the complete resolved ``(source_idx, target_idx)``
  mapping for adopt-theme reconciliation; ``target_idx`` is ``None`` for an
  orphan. Empty for keep-appearance and bake.
* ``parts_added`` -- partnames added to the destination package by this import.
* ``parts_reused`` -- partnames of existing destination parts reused via
  content-hash deduplication (keep_appearance).
* ``notes_copied`` -- whether the source slide's speaker-notes part was imported.
* ``comments_dropped`` -- count of comment parts dropped (comments never travel).
* ``section`` / ``section_id`` -- name and exact GUID of the destination section the
  slide was enrolled in, or None when it was not enrolled in a section.
* ``baked_shapes`` -- names of placeholders converted to free baked shapes.
* ``dropped_placeholders`` -- names of furniture placeholders (dt/ftr/sldNum)
  removed under "bake".
* ``run_shifts`` -- `pptx.rebind.RunShift` entries for every run whose
  resolved appearance changed (populated for "adopt_theme"; empty for
  keep_appearance).

### `baked_shapes`

```python
baked_shapes: Tuple[str, ...]
```

### `comments_dropped`

```python
comments_dropped: int
```

### `dest_slide`

```python
dest_slide: str
```

### `dest_slide_id`

```python
dest_slide_id: int
```

### `dropped_placeholders`

```python
dropped_placeholders: Tuple[str, ...]
```

### `layout_binding`

```python
layout_binding: str
```

### `layout_binding_method`

```python
layout_binding_method: str
```

### `mode`

```python
mode: str
```

### `notes_copied`

```python
notes_copied: bool
```

### `parts_added`

```python
parts_added: Tuple[str, ...]
```

### `parts_reused`

```python
parts_reused: Tuple[str, ...]
```

### `placeholder_map_used`

```python
placeholder_map_used: Tuple[Tuple[int, Optional[int]], ...]
```

### `position`

```python
position: int
```

### `run_shifts`

```python
run_shifts: tuple
```

### `section`

```python
section: Optional[str]
```

### `section_id`

```python
section_id: Optional[str]
```

### `source_slide`

```python
source_slide: str
```

### `to_dict`

```python
to_dict() -> dict
```

Return the import report as a JSON-ready dict stamped with its schema and version.

## `PackageTransaction`

```python
PackageTransaction(package: 'OpcPackage', *roots: object)
```

Restore a package's original live graph if the guarded operation raises.

Prepare a rollback boundary over `package`, tracking the proxies passed as `roots`.

Validates the relationship graph immediately, so a malformed relationship or a signature
part refuses here rather than on entry. Nothing is snapshotted until `__enter__`, so the
instance can be built well before the mutation site.

## `SCHEMA_NAME`

```python
SCHEMA_NAME = 'paper-import-report'
```

## `SCHEMA_VERSION`

```python
SCHEMA_VERSION = 3
```

## `append_deck`

```python
append_deck(dest_prs: 'Presentation', source_prs: 'Presentation', *, mode: str, notes: bool = True) -> 'Tuple[ImportReport, ...]'
```

Import every `source_prs` slide, in order, at the end of `dest_prs`.

The COMPLETE source deck validates before the first destination write: a refusal on
any source slide leaves the destination untouched.

## `import_slide`

```python
import_slide(dest_prs: 'Presentation', source_prs: 'Presentation', slide: 'Presentation', *, mode: str, position: 'Optional[int]' = None, notes: bool = True, section: 'Optional[str]' = None, section_id: 'Optional[str]' = None, target_layout: 'Optional[SlideLayout]' = None, placeholder_map: 'Optional[SlideLayout]' = 'auto') -> ImportReport
```

Import one slide from `source_prs` into `dest_prs`; return the `ImportReport`.
