<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.opc.pkgwriter`

Provides low-level, write-only API to serialized (OPC) package.

## `PackageWriter`

Writes a zip-format OPC package to `pkg_file`, where `pkg_file` can be either a
path to a zip file (a string) or a file-like object.

Its single API method, `write`, is static, so this class is not intended to be
instantiated.

### `write`

```python
write(pkg_file, pkg_rels, parts)
```

Write `parts` and `pkg_rels` to `pkg_file` as a .docx package, with a content-types
stream derived from the parts.
