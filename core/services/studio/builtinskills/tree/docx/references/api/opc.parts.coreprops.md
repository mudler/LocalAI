<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.opc.parts.coreprops`

Core properties part, corresponds to ``/docProps/core.xml`` part in package.

## `CorePropertiesPart`

Bases: `XmlPart`

Corresponds to part named ``/docProps/core.xml``.

The "core" is short for "Dublin Core" and contains document metadata relatively common across
documents of all types, not just DOCX.

### `core_properties`

```python
core_properties
```

A `CoreProperties` object providing read/write access to the core properties
contained in this core properties part.

### `default`

```python
default(package: OpcPackage)
```

Return a new `CorePropertiesPart` object initialized with default values for
its base properties.
