<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.controls`

Content-control (structured document tag) surface — the templating

## `Control`

```python
Control(sdt: '_Element', story: str, document: 'Document') -> None
```

Live proxy for one content control.

### `alias`

```python
alias: Optional[str]
```

### `control_type`

```python
control_type: str
```

### `info`

```python
info() -> ControlInfo
```

### `is_data_bound`

```python
is_data_bound: bool
```

### `set_value`

```python
set_value(value: 'Union[str, bool, dt.date, dt.datetime]') -> None
```

Set the control's value, validating fully before mutating anything.

Text and rich-text controls take a string, checkboxes a bool, dropdowns a value from
their `w:listItem` choices, dates an ISO date. Refuses a protected document, a stale or
foreign control, a locked or data-bound surface, a picture, group or building-block
control, a dropdown value outside its choices, and an unsupported date format.

### `showing_placeholder`

```python
showing_placeholder: bool
```

### `tag`

```python
tag: Optional[str]
```

### `value`

```python
value: 'Union[str, bool]'
```

The control's current value (bool for checkboxes, text otherwise).

## `ControlInfo`

```python
ControlInfo(tag: Optional[str], alias: Optional[str], control_type: str, value: str, story: str, showing_placeholder: bool, is_data_bound: bool, choices: Tuple[str, ...]) -> None
```

Identity and current state of one content control.

### `alias`

```python
alias: Optional[str]
```

### `choices`

```python
choices: Tuple[str, ...]
```

### `control_type`

```python
control_type: str
```

### `is_data_bound`

```python
is_data_bound: bool
```

### `showing_placeholder`

```python
showing_placeholder: bool
```

### `story`

```python
story: str
```

### `tag`

```python
tag: Optional[str]
```

### `to_dict`

```python
to_dict() -> dict
```

### `value`

```python
value: str
```

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.

## `get_control`

```python
get_control(document: 'Document', *, tag: Optional[str] = None, alias: Optional[str] = None) -> Control
```

The single control matching `tag` and/or `alias`, or a typed refusal.

## `iter_controls`

```python
iter_controls(document: 'Document') -> 'Iterator[Control]'
```

Every content control in every story part, document order.

## `list_controls`

```python
list_controls(document: 'Document') -> Tuple[ControlInfo, ...]
```

Identity and state of every control (schema-stable via .to_dict()).

## `rollback_on_error`

```python
rollback_on_error(document: 'Document', *participants: Any) -> Generator[None, None, None]
```

Restore the live package and named mutable proxies after an error.

## `set_control_value`

```python
set_control_value(document: 'Document', value: 'Union[str, bool, dt.date, dt.datetime]', *, tag: Optional[str] = None, alias: Optional[str] = None) -> None
```

Set the value of the control that `tag` names.

Refuses when no control carries the tag and when several do, plus every refusal
`Control.set_value` raises.
