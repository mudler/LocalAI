<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.styles`

Sub-package module for docx.styles sub-package.

## `BabelFish`

Translates special-case style names from UI name (e.g. Heading 1) to
internal/styles.xml name (e.g. heading 1) and back.

### `internal2ui`

```python
internal2ui(internal_style_name: str) -> str
```

Return the user interface style name corresponding to `internal_style_name`,
such as 'Heading 1' for 'heading 1'.

### `internal_style_names`

```python
internal_style_names: Dict[str, str] = dict(style_aliases)
```

### `style_aliases`

```python
style_aliases = (('Caption', 'caption'), ('Footer', 'footer'), ('Header', 'header'), ('Heading 1', 'heading 1'), ('Heading 2', 'heading 2'), ('Heading 3', 'heading 3'), ('Heading 4', 'heading 4'), ('Heading 5', 'heading 5'), ('Heading 6', 'heading 6'), ('Heading 7', 'heading 7'), ('Heading 8', 'heading 8'), ('Heading 9', 'heading 9'))
```

### `ui2internal`

```python
ui2internal(ui_style_name: str) -> str
```

Return the internal style name corresponding to `ui_style_name`, such as
'heading 1' for 'Heading 1'.

### `ui_style_names`

```python
ui_style_names = {item[1]: item[0] for item in style_aliases}
```
