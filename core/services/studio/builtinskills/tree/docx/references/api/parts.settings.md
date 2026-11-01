<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.parts.settings`

`SettingsPart` and closely related objects.

## `SettingsPart`

```python
SettingsPart(partname: PackURI, content_type: str, element: CT_Settings, package: Package)
```

Bases: `XmlPart`

Document-level settings part of a WordprocessingML (WML) package.

### `default`

```python
default(package: Package)
```

Return a newly created settings part, containing a default `w:settings` element tree.

### `settings`

```python
settings: Settings
```

A `Settings` proxy object for the `w:settings` element in this part.

Contains the document-level settings for this document.
