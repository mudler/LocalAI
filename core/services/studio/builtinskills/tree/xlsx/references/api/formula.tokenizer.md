<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.formula.tokenizer`

This module contains a tokenizer for Excel formulae.

## `EXCEL_ERROR_CODES`

```python
EXCEL_ERROR_CODES = ('#NULL!', '#DIV/0!', '#VALUE!', '#REF!', '#NAME?', '#NUM!', '#N/A', '#SPILL!', '#CALC!', '#FIELD!', '#BLOCKED!', '#UNKNOWN!', '#CONNECT!', '#BUSY!', '#PYTHON!', '#GETTING_DATA')
```

## `Token`

```python
Token(value, type_, subtype = '')
```

A token in an Excel formula.

Tokens have three attributes:

* `value`: The string value parsed that led to this token
* `type`: A string identifying the type of token
* `subtype`: A string identifying subtype of the token (optional, and
             defaults to "")

### `ARG`

```python
ARG = 'ARG'
```

### `ARRAY`

```python
ARRAY = 'ARRAY'
```

### `CLOSE`

```python
CLOSE = 'CLOSE'
```

### `ERROR`

```python
ERROR = 'ERROR'
```

### `FUNC`

```python
FUNC = 'FUNC'
```

### `LITERAL`

```python
LITERAL = 'LITERAL'
```

### `LOGICAL`

```python
LOGICAL = 'LOGICAL'
```

### `NUMBER`

```python
NUMBER = 'NUMBER'
```

### `OPEN`

```python
OPEN = 'OPEN'
```

### `OPERAND`

```python
OPERAND = 'OPERAND'
```

### `OP_IN`

```python
OP_IN = 'OPERATOR-INFIX'
```

### `OP_POST`

```python
OP_POST = 'OPERATOR-POSTFIX'
```

### `OP_PRE`

```python
OP_PRE = 'OPERATOR-PREFIX'
```

### `PAREN`

```python
PAREN = 'PAREN'
```

### `RANGE`

```python
RANGE = 'RANGE'
```

### `ROW`

```python
ROW = 'ROW'
```

### `SEP`

```python
SEP = 'SEP'
```

### `TEXT`

```python
TEXT = 'TEXT'
```

### `WSPACE`

```python
WSPACE = 'WHITE-SPACE'
```

### `get_closer`

```python
get_closer()
```

Return a closing token that matches this token's type.

### `make_operand`

```python
make_operand(value)
```

Create an operand token.

### `make_separator`

```python
make_separator(value)
```

Create a separator token

### `make_subexp`

```python
make_subexp(value, func = False)
```

Create a subexpression token.

`value`: The value of the token
`func`: If True, force the token to be of type FUNC

### `subtype`

```python
subtype = subtype
```

### `type`

```python
type = type_
```

### `value`

```python
value = value
```

## `Tokenizer`

```python
Tokenizer(formula)
```

A tokenizer for Excel worksheet formulae.

Converts a str string representing an Excel formula (in A1 notation)
into a sequence of `Token` objects.

`formula`: The str string to tokenize

Tokenizer defines a method `._parse()` to parse the formula into tokens,
which can then be accessed through the `.items` attribute.

### `ERROR_CODES`

```python
ERROR_CODES = EXCEL_ERROR_CODES
```

### `SN_RE`

```python
SN_RE = re.compile('^[1-9](\\.[0-9]+)?[Ee]$')
```

### `STRING_REGEXES`

```python
STRING_REGEXES = {'"': re.compile('"(?:[^"]*"")*[^"]*"(?!")'), "'": re.compile("'(?:[^']*'')*[^']*'(?!')")}
```

### `TOKEN_ENDERS`

```python
TOKEN_ENDERS = ',;}) +-*/^&=><%'
```

### `WSPACE_RE`

```python
WSPACE_RE = re.compile('[ \\n]+')
```

### `assert_empty_token`

```python
assert_empty_token(can_follow = ())
```

Ensure that there's no token currently being parsed.

Or if there is a token being parsed, it must end with a character in
can_follow.

If there are unconsumed token contents, it means we hit an unexpected
token transition. In this case, we raise a TokenizerError

### `check_scientific_notation`

```python
check_scientific_notation()
```

Consumes a + or - character if part of a number in sci. notation.

Returns True if the character was consumed and self.offset was
updated, False otherwise.

### `formula`

```python
formula = formula
```

### `items`

```python
items = []
```

### `offset`

```python
offset = 0
```

### `render`

```python
render()
```

Convert the parsed tokens back to a string.

### `save_token`

```python
save_token()
```

If there's a token being parsed, add it to the item list.

### `token`

```python
token = []
```

### `token_stack`

```python
token_stack = []
```

## `TokenizerError`

Bases: `Exception`

Base class for all Tokenizer errors.
