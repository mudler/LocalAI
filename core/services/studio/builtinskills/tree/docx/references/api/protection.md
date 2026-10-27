<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.protection`

Document-protection awareness (paper-docx).

## `OPERATION_CLASSES`

```python
OPERATION_CLASSES = (OP_COMMENT, OP_FORM_FIELD, OP_BODY)
```

## `OP_BODY`

```python
OP_BODY = 'body-content'
```

## `OP_COMMENT`

```python
OP_COMMENT = 'comment'
```

## `OP_FORM_FIELD`

```python
OP_FORM_FIELD = 'form-field-value'
```

## `ProtectionStatus`

```python
ProtectionStatus(edit: Optional[str], enforced: bool, acknowledged: bool, formatting: bool = False) -> None
```

What `w:documentProtection` declares, read-only.

`edit` is the raw `w:edit` token ("readOnly", "forms", "comments",
"trackedChanges", or unrestricted "none") or None when no edit restriction is declared;
`formatting` reflects the independent format restriction. `enforced`
reports active enforcement of either kind; `acknowledged` is this
package's in-memory override flag (never persisted).

### `acknowledged`

```python
acknowledged: bool
```

### `blocks_paper_edits`

```python
blocks_paper_edits: bool
```

True when protection is enforced and has not been acknowledged.

Not a prediction of whether a call will refuse: it ignores the operation class, so it
reports True under trackedChanges or formatting-only protection where the gate still
permits body edits and comments. Call `acknowledge_protection` to proceed deliberately.

### `edit`

```python
edit: Optional[str]
```

### `enforced`

```python
enforced: bool
```

### `formatting`

```python
formatting: bool = False
```

### `to_dict`

```python
to_dict() -> dict
```

## `acknowledge_protection`

```python
acknowledge_protection(document: 'Document') -> ProtectionStatus
```

Explicitly override protection for this open package (in-memory only).

The single sanctioned override affordance: after this call, paper-docx
mutating APIs proceed on this document despite `w:documentProtection`.
Nothing is written to the file — the protection setting itself is never
touched. Returns the status being overridden so callers can log it.

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.

## `protection_status`

```python
protection_status(document: 'Document') -> ProtectionStatus
```

The document's `w:documentProtection` state (report-only).

## `rollback_on_error`

```python
rollback_on_error(document: 'Document', *participants: Any) -> Generator[None, None, None]
```

Restore the live package and named mutable proxies after an error.

## `set_protection`

```python
set_protection(document: 'Document', *, edit: str) -> ProtectionStatus
```

Turn Restrict Editing on for delivery (`w:documentProtection`).

`edit` is a Word token: `readOnly`, `comments`, `forms`, or
`trackedChanges`. This writes the setting; it does not strip one.
Paper mutators then refuse until `acknowledge_protection`.
