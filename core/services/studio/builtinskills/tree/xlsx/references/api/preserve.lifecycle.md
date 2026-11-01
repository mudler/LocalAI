<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.lifecycle`

One primitive, both directions: every part a save creates or deletes

## `PartPlan`

```python
PartPlan(existing_names)
```

Adds/removals of whole parts for one save, with their content-type
and relationship consequences planned in lockstep.

### `add_default`

```python
add_default(extension, content_type)
```

### `add_part`

```python
add_part(name, payload, content_type = None, relate_from = None, rel_type = None, rel_id = None)
```

Plan a new part; returns the allocated relationship id (or
``rel_id`` when given). ``relate_from`` names the part whose rels
must point at the new part (its rels part is created if absent —
the saver resolves ids at apply time via ``resolve_rel_ids``).

### `added`

```python
added = {}
```

### `apply_content_types`

```python
apply_content_types(payload)
```

### `apply_rels`

```python
apply_rels(rels_part, payload)
```

The updated payload for one rels part (``payload`` is None when
the part does not exist yet — a fresh rels document is built).

### `ct_defaults`

```python
ct_defaults = []
```

### `ct_overrides`

```python
ct_overrides = []
```

### `ct_removals`

```python
ct_removals = []
```

### `dropped`

```python
dropped = set()
```

### `existing`

```python
existing = set(existing_names)
```

### `rel_appends`

```python
rel_appends = {}
```

### `rel_removals`

```python
rel_removals = {}
```

### `remove_part`

```python
remove_part(name, referencing_rels = ())
```

Plan a part's removal: dropped from the copy loop, its
content-type override removed, and the named relationships cut.
``referencing_rels`` is [(rels_part, target_suffix)].

### `reserve_rid`

```python
reserve_rid(rels_part, existing_payload)
```

Sequential rId allocation shared by every planner touching one
rels part (two independent next_rid computations collide).

### `touched_rels_parts`

```python
touched_rels_parts()
```
