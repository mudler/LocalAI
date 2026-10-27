<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.lexical`

Patch modeled XML changes without reserializing unchanged syntax.

## `patch_xml`

```python
patch_xml(original, baseline, current, expected_root)
```

Return ``original`` with only model-observed lexical deltas applied.

``None`` means the edit changed element shape or could not be mapped
unambiguously.  Callers may then use a guarded structural fallback, but
must not silently normalize XML that carries unowned content.

**Parameters:**

- **original** (`bytes`) – Original XML bytes to preserve.
- **baseline** (`bytes`) – Model serialization captured before editing.
- **current** (`bytes`) – Model serialization after editing.
- **expected_root** (`str`) – Expected local name of the XML root element.

**Returns:**

- `bytes | None` – Patched XML bytes, or ``None`` when a safe patch is unavailable.
