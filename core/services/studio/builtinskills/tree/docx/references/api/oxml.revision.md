<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.revision`

Custom element classes for the tracked-change (revision) vocabulary.

## `CT_RunTrackChange`

Bases: `BaseOxmlElement`

`w:ins` and `w:del` elements: a tracked run-level change.

Both tags share this shape (ECMA-376 CT_RunTrackChange): required unique
`w:id`, required `w:author`, optional `w:date`, containing the changed
runs. `w:del` also appears childless inside `w:pPr/w:rPr` to mark a
paragraph mark as deleted; ``ZeroOrMore`` runs covers that use too.

### `add_tracked_run`

```python
add_tracked_run(text: str, rpr: 'Optional[_Element]', *, deleted: bool) -> 'CT_R'
```

Append a run holding `text`, with `rpr` cloned in when given.

Deleted text goes into `w:delText` (never live `w:t` inside `w:del`);
inserted text into `w:t`. Edge whitespace gets `xml:space="preserve"`.

### `author`

```python
author = RequiredAttribute('w:author', ST_String)
```

### `date`

```python
date = OptionalAttribute('w:date', ST_DateTime)
```

### `id`

```python
id = RequiredAttribute('w:id', ST_DecimalNumber)
```

### `new`

```python
new(tag: str, revision_id: int, author: str, date: Optional[dt.datetime]) -> 'CT_RunTrackChange'
```

A new `w:ins` or `w:del` element with its identity attributes set.

### `r`

```python
r = ZeroOrMore('w:r')
```
