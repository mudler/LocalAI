<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.formatting`

Effective-format resolver — read-only, provenance-bearing.

## `EffectiveFormat`

```python
EffectiveFormat(properties: Dict[str, ResolvedValue], unresolved: Tuple[str, ...]) -> None
```

The resolved property map, plus what is declared unresolvable.

### `properties`

```python
properties: Dict[str, ResolvedValue]
```

### `to_dict`

```python
to_dict() -> dict
```

### `unresolved`

```python
unresolved: Tuple[str, ...]
```

### `value_of`

```python
value_of(key: str)
```

## `ResolvedValue`

```python
ResolvedValue(value: object, source: str, chain: Tuple[str, ...] = ()) -> None
```

One property's effective value and where it came from.

`source` is "direct", "character_style:<id>", "paragraph_style:<id>", "doc_defaults",
"toggle_xor", "agreeing_layers", "mixed", or "none". `chain` holds the contributing
layers.

### `chain`

```python
chain: Tuple[str, ...] = ()
```

### `source`

```python
source: str
```

### `to_dict`

```python
to_dict() -> dict
```

### `value`

```python
value: object
```

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.

## `format_of`

```python
format_of(target) -> EffectiveFormat
```

The effective formatting of a `Run`, `Paragraph` or `Span`.

Read-only and provenance-bearing: each `ResolvedValue` says where its value came from.
Runs resolve the supported run-property subset; paragraphs resolve alignment and style
plus the run defaults their style chain implies; spans resolve every run they touch and
report "mixed" where runs disagree. Raises `TypeError` for anything else.
