<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.opc.shared`

Objects shared by modules in the pptx.opc sub-package.

## `CaseInsensitiveDict`

Bases: `dict`

Mapping type like dict except it matches key without respect to case.

For example, D['A'] == D['a']. Note this is not general-purpose, just complete
enough to satisfy opc package needs. It assumes str keys for example.
