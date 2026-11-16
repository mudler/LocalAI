"""CSS selector parser."""
from __future__ import annotations
import re
from functools import lru_cache
from . import util
from . import css_match as cm
from . import css_types as ct
from .util import SelectorSyntaxError
import warnings
from typing import Match, Any, Callable, Iterator, Iterable, Mapping, cast
from dataclasses import dataclass
from collections import UserDict
import threading

__all__ = ('SelectorSyntaxError',)

RE_LOCK = threading.Lock()
SEL_LOCK = threading.RLock()

UNICODE_REPLACEMENT_CHAR = 0xFFFD

# Non-ASCII identifier code point ranges (inclusive): https://drafts.csswg.org/css-syntax-3/#non-ascii-ident-code-point
NON_ASCII_IDENT_RANGES = (
    (0x00B7, 0x00B7),
    (0x00C0, 0x00D6),
    (0x00D8, 0x00F6),
    (0x00F8, 0x037D),
    (0x037F, 0x1FFF),
    (0x200C, 0x200D),
    (0x203F, 0x2040),
    (0x2070, 0x218F),
    (0x2C00, 0x2FEF),
    (0x3001, 0xD7FF),
    (0xF900, 0xFDCF),
    (0xFDF0, 0xFFFD),
    (0x10000, 0x10FFFF)
)

SELECTOR_LIMIT = 8192

# Simple pseudo classes that take no parameters
PSEUDO_SIMPLE = {
    ":any-link",
    ":empty",
    ":first-child",
    ":first-of-type",
    ":in-range",
    ":open",
    ":out-of-range",
    ":last-child",
    ":last-of-type",
    ":link",
    ":only-child",
    ":only-of-type",
    ":root",
    ':checked',
    ':default',
    ':disabled',
    ':enabled',
    ':indeterminate',
    ':optional',
    ':placeholder-shown',
    ':read-only',
    ':read-write',
    ':required',
    ':scope',
    ':defined',
    ':muted'
}

# Supported, simple pseudo classes that match nothing in the Soup Sieve environment
PSEUDO_SIMPLE_NO_MATCH = {
    ':active',
    ':autofill',
    ':buffering',
    ':current',
    ':focus',
    ':focus-visible',
    ':focus-within',
    ':fullscreen',
    ':future',
    ':host',
    ':hover',
    ':local-link',
    ':past',
    ':paused',
    ':picture-in-picture',
    ':playing',
    ':popover-open',
    ':seeking',
    ':stalled',
    ':target',
    ':target-within',
    ':user-invalid',
    ':volume-locked',
    ':visited'
}

# Attributes whose values are matched case insensitively on HTML elements in HTML documents
# https://html.spec.whatwg.org/multipage/semantics-other.html#case-sensitivity-of-selectors
HTML_CASE_INSENSITIVE_ATTRIBUTES = frozenset(
    (
        'accept', 'accept-charset', 'align', 'alink', 'axis', 'bgcolor', 'charset', 'checked', 'clear',
        'codetype', 'color', 'compact', 'declare', 'defer', 'dir', 'direction', 'disabled', 'enctype',
        'face', 'frame', 'hreflang', 'http-equiv', 'lang', 'language', 'link', 'media', 'method',
        'multiple', 'nohref', 'noresize', 'noshade', 'nowrap', 'readonly', 'rel', 'rev', 'rules', 'scope',
        'scrolling', 'selected', 'shape', 'target', 'text', 'type', 'valign', 'valuetype', 'vlink'
    )
)

# Complex pseudo classes that take selector lists
PSEUDO_COMPLEX = {
    ':contains',
    ':-soup-contains',
    ':-soup-contains-own',
    ':has',
    ':is',
    ':matches',
    ':not',
    ':where'
}

PSEUDO_COMPLEX_NO_MATCH = {
    ':current',
    ':host',
    ':host-context'
}

# Complex pseudo classes that take very specific parameters and are handled special
PSEUDO_SPECIAL = {
    ':dir',
    ':lang',
    ':nth-child',
    ':nth-last-child',
    ':nth-last-of-type',
    ':nth-of-type'
}

PSEUDO_SUPPORTED = PSEUDO_SIMPLE | PSEUDO_SIMPLE_NO_MATCH | PSEUDO_COMPLEX | PSEUDO_COMPLEX_NO_MATCH | PSEUDO_SPECIAL

# Sub-patterns parts
# Whitespace
NEWLINE = r'(?>\r\n|[\n\f\r])'
WS = fr'(?:[ \t]|{NEWLINE})'
# Comments
COMMENTS = r'(?:/\*(?:[^*]|\*(?!/))*+\*/)'
# Whitespace with comments included
WSC = fr'(?:{WS}|{COMMENTS})'
# CSS escapes
CSS_ESCAPES = fr'(?:\\(?:[a-f0-9]{{1,6}}+{WS}?+|[^\r\n\f]|$))'
CSS_STRING_ESCAPES = fr'(?:\\(?:[a-f0-9]{{1,6}}+{WS}?+|{NEWLINE}|[^\r\n\f]|$))'
# CSS identifier code points (browsers treat all non-ASCII code points as identifier code points)
IDENT_START = r'[A-Za-z_\x80-\U0010ffff]'
IDENT_CHAR = r'[-0-9A-Za-z_\x80-\U0010ffff]'
# CSS Identifier
IDENTIFIER = fr'''
(?:(?:--|-?+(?:{IDENT_START}|{CSS_ESCAPES}))
(?:{IDENT_CHAR}|{CSS_ESCAPES})*+)
'''
# `nth` content
NTH = fr'[-+]?+(?:[0-9]++n?+|n)(?:(?<=n){WSC}*+[-+]{WSC}*+[0-9]++)?+'
# Value: quoted string or identifier
VALUE = fr'''
(?:"(?:\\(?:{NEWLINE}|[^\r\n\f])|[^\\"\r\n\f])*+"|
'(?:\\(?:{NEWLINE}|[^\r\n\f])|[^\\'\r\n\f])*+'|
{IDENTIFIER})
'''
# Attribute value comparison. `!=` is handled special as it is non-standard.
ATTR = fr'(?:{WSC}*+(?P<cmp>[!~^|*$]?+=){WSC}*+(?P<value>{VALUE})(?:{WSC}*+(?P<case>[is]))?+)?+'

# Selector patterns
# IDs (`#id`)
PAT_ID = fr'\#{IDENTIFIER}'
# Classes (`.class`)
PAT_CLASS = fr'\.{IDENTIFIER}'
# Prefix:Tag (`prefix|tag`)
PAT_TAG = fr'(?P<tag_ns>(?:{IDENTIFIER}|\*)?+\|)?+(?P<tag_name>{IDENTIFIER}|\*)'
# Attributes (`[attr]`, `[attr=value]`, etc.)
PAT_ATTR = fr'\[{WSC}*+(?P<attr_ns>(?:{IDENTIFIER}|\*)?+\|(?!=))?+(?P<attr_name>{IDENTIFIER}){ATTR}{WSC}*+\]'
# Pseudo class (`:pseudo-class`, `:pseudo-class(`)
PAT_PSEUDO_CLASS = fr'(?P<name>:{IDENTIFIER})(?P<open>\({WSC}*+)?+'
# Pseudo class special patterns. Matches `:pseudo-class(` for special case pseudo classes.
PAT_PSEUDO_CLASS_SPECIAL = fr'(?P<name>:{IDENTIFIER})(?P<open>\({WSC}*+)'
# Custom pseudo class (`:--custom-pseudo`)
PAT_PSEUDO_CLASS_CUSTOM = fr'(?P<name>:(?=--){IDENTIFIER})'
# Nesting ampersand selector. Matches `&`
PAT_AMP = r'&'
# Closing pseudo group (`)`)
PAT_PSEUDO_CLOSE = fr'{WSC}*+\)'
# Pseudo element (`::pseudo-element`)
PAT_PSEUDO_ELEMENT = fr':{PAT_PSEUDO_CLASS}'
# At rule (`@page`, etc.) (not supported)
PAT_AT_RULE = fr'@P{IDENTIFIER}'
# Pseudo class `nth-child` (`:nth-child(an+b [of S]?)`, `:first-child`, etc.)
PAT_PSEUDO_NTH_CHILD = fr'''
(?P<pseudo_nth_child>{PAT_PSEUDO_CLASS_SPECIAL}
(?P<nth_child>{NTH}|even|odd))(?:{WSC}*+\)|(?P<of>{COMMENTS}*+{WS}{WSC}*+of{COMMENTS}*+{WS}{WSC}*+))
'''
# Pseudo class `nth-of-type` (`:nth-of-type(an+b)`, `:first-of-type`, etc.)
PAT_PSEUDO_NTH_TYPE = fr'''
(?P<pseudo_nth_type>{PAT_PSEUDO_CLASS_SPECIAL}
(?P<nth_type>{NTH}|even|odd)){WSC}*+\)
'''
# Pseudo class language (`:lang("*-de", en)`)
PAT_PSEUDO_LANG = fr'{PAT_PSEUDO_CLASS_SPECIAL}(?P<values>{VALUE}(?:{WSC}*+,{WSC}*+{VALUE})*+){WSC}*+\)'
# Pseudo class direction (`:dir(ltr)`)
PAT_PSEUDO_DIR = fr'{PAT_PSEUDO_CLASS_SPECIAL}(?P<dir>{IDENTIFIER}){WSC}*+\)'
# Combining characters (`>`, `~`, ` `, `+`, `,`) along with any comments and whitespace around them.
# A run of comments and whitespace is matched all at once. `ws` captures whether the run has whitespace
# before the relation, as a run of only comments does not combine selectors.
PAT_COMBINE = fr'''
(?=[,+>~]|{WS}|/\*){COMMENTS}*+(?:(?P<ws>{WS}){WSC}*+)?+(?P<relation>[,+>~])?+{WSC}*+
'''
# Extra: Contains (`:contains(text)`)
PAT_PSEUDO_CONTAINS = fr'{PAT_PSEUDO_CLASS_SPECIAL}(?P<values>{VALUE}(?:{WSC}*+,{WSC}*+{VALUE})*+){WSC}*+\)'

# Regular expressions
# CSS escape pattern
RE_CSS_ESC = re.compile(fr'(?:(\\[a-f0-9]{{1,6}}+{WSC}?+)|(\\[^\r\n\f])|(\\$))', re.I)
RE_CSS_STR_ESC = re.compile(fr'(?:(\\[a-f0-9]{{1,6}}+{WS}?+)|(\\[^\r\n\f])|(\\$)|(\\{NEWLINE}))', re.I)
# Pattern to break up `nth` specifiers
RE_NTH = re.compile(fr'(?P<s1>[-+])?+(?P<a>[0-9]++n?+|n)(?:(?<=n){WSC}*+(?P<s2>[-+]){WSC}*+(?P<b>[0-9]++))?+', re.I)
# Pattern to iterate multiple values.
RE_VALUES = re.compile(fr'(?:(?P<value>{VALUE})|(?P<split>{WSC}*+,{WSC}*+))', re.X)
# Whitespace checks
RE_WS = re.compile(WS)
RE_CUSTOM = re.compile(fr'^{PAT_PSEUDO_CLASS_CUSTOM}$', re.X)
RE_PSEUDO_CLASS_SPECIAL = re.compile(PAT_PSEUDO_CLASS_SPECIAL, re.I | re.X | re.U)
RE_PSEUDO_IGNORE = re.compile(PAT_PSEUDO_CLASS, re.X | re.I | re.U)

QUOTED = ("'", '"')

# Constants
# List split token
COMMA_COMBINATOR = ','
# Relation token for descendant
WS_COMBINATOR = " "

# Parse flags
FLG_PSEUDO = 0x01
FLG_NOT = 0x02
FLG_RELATIVE = 0x04
FLG_DEFAULT = 0x08
FLG_HTML = 0x10
FLG_INDETERMINATE = 0x20
FLG_OPEN = 0x40
FLG_IN_RANGE = 0x80
FLG_OUT_OF_RANGE = 0x100
FLG_PLACEHOLDER_SHOWN = 0x200
FLG_FORGIVE = 0x400
FLG_EDITABLE = 0x800
FLG_CHECKED = 0x1000

# Maximum cached patterns to store
_MAXCACHE = 500


@lru_cache(maxsize=_MAXCACHE)
def _cached_css_compile(
    pattern: str,
    namespaces: ct.Namespaces | None,
    custom: ct.CustomSelectors | None,
    ignore: tuple[str] | None = None,
    flags: int = 0,
    max_selectors: int = SELECTOR_LIMIT
) -> cm.SoupSieve:
    """Cached CSS compile."""

    return cm.SoupSieve(
        pattern,
        CSSParser(
            pattern,
            custom=custom,
            ignore=ignore,
            flags=flags,
            max_selectors=max_selectors
        ).process_selectors(),
        namespaces,
        custom,
        flags
    )


def _purge_cache() -> None:
    """Purge the cache."""

    _cached_css_compile.cache_clear()


def css_unescape(content: str) -> str:
    """
    Unescape CSS value.

    Strings allow for spanning the value on multiple strings by escaping a new line.
    """

    def replace(m: Match[str]) -> str:
        """Replace with the appropriate substitute."""

        if m.group(1):
            codepoint = int(m.group(1)[1:], 16)
            # A surrogate, or a value past Unicode, is `U+FFFD`. Zero already is.
            if (
                codepoint == 0
                or codepoint > 0x10FFFF
                or 0xD800 <= codepoint <= 0xDFFF
            ):
                codepoint = UNICODE_REPLACEMENT_CHAR
            value = chr(codepoint)
        elif m.group(2):
            value = m.group(2)[1:]
        elif m.group(3):
            value = '\ufffd'
        else:
            value = ''

        return value

    quoted = content.startswith(QUOTED)
    return (RE_CSS_STR_ESC if quoted else RE_CSS_ESC).sub(
        replace,
        content[1:-1] if quoted else content
    )


def escape(ident: str) -> str:
    """Escape identifier."""

    string = []
    length = len(ident)
    start_dash = length > 0 and ident[0] == '-'
    if length == 1 and start_dash:
        # Need to escape identifier that is a single `-` with no other characters
        string.append(f'\\{ident}')
    else:
        for index, c in enumerate(ident):
            codepoint = ord(c)
            if codepoint == 0x00:
                string.append('\ufffd')
            elif (0x01 <= codepoint <= 0x1F) or codepoint == 0x7F:
                string.append(f'\\{codepoint:x} ')
            elif (index == 0 or (start_dash and index == 1)) and (0x30 <= codepoint <= 0x39):
                string.append(f'\\{codepoint:x} ')
            elif (
                codepoint in (0x2D, 0x5F) or (0x30 <= codepoint <= 0x39) or
                (0x41 <= codepoint <= 0x5A) or (0x61 <= codepoint <= 0x7A) or
                (codepoint >= 0x80 and any(start <= codepoint <= end for start, end in NON_ASCII_IDENT_RANGES))
            ):
                string.append(c)
            else:
                string.append(f'\\{c}')
    return ''.join(string)


class SelectorPattern:
    """
    Selector pattern.

    Patterns are shared by all parsers, so they must not hold any per-match state.
    `match` returns the token name along with the match; a name of `None` means the
    match is consumed without being processed further.
    """

    def __init__(self, name: str, pattern: str) -> None:
        """Initialize."""

        self.name = name
        self.pattern = pattern
        self._re_pattern: re.Pattern[str] | None = None

    @property
    def re_pattern(self) -> re.Pattern[str]:
        """Retrieve the compiled regular expression pattern."""

        with RE_LOCK:
            if self._re_pattern is None:
                self._re_pattern = re.compile(self.pattern, re.I | re.X | re.U)
        return self._re_pattern

    def match(self, selector: str, index: int, flags: int) -> tuple[str | None, Match[str]] | None:
        """Match the selector."""

        m = self.re_pattern.match(selector, index)
        return None if m is None else (self.name, m)


class SelectorPatternCombinator(SelectorPattern):
    """
    A run of comments and whitespace might be a combinator or might just be consumed.

    Process all combinators except whitespace at the start or end, and runs of only comments.
    """

    def match(self, selector: str, index: int, flags: int) -> tuple[str | None, Match[str]] | None:
        """
        Match the selector.

        If we match at the start or end of the string and have a non-whitespace combinator, the match is bogus.
        If we are at the start or end and the combinator is white space, then it can be consumed with no further
        processing. Comments are not whitespace, so a run of only comments is consumed as well.
        """

        m = self.re_pattern.match(selector, index)
        # An unterminated comment matches nothing, which must not be consumed or the parser would never advance.
        if m is None or m.end(0) == index:
            return None
        relation = m.group('relation')
        if m.start(0) == 0 or m.end(0) == len(selector):
            return None if relation else (None, m)
        return (self.name if relation or m.group('ws') else None), m


class SpecialPseudoPattern(SelectorPattern):
    """Selector pattern."""

    def __init__(self, patterns: tuple[tuple[str, tuple[str, ...], str, type[SelectorPattern]], ...]) -> None:
        """Initialize."""

        self.patterns = {}
        for p in patterns:
            name = p[0]
            pattern = p[3](name, p[2])
            for pseudo in p[1]:
                self.patterns[pseudo] = pattern

    def match(self, selector: str, index: int, flags: int) -> tuple[str | None, Match[str]] | None:
        """Match the selector."""

        m = RE_PSEUDO_CLASS_SPECIAL.match(selector, index)
        if m:
            pattern = self.patterns.get(util.lower(css_unescape(m.group('name'))))
            if pattern:
                return pattern.match(selector, index, flags)
        return None


class _Selector:
    """
    Intermediate selector class.

    This stores selector data for a compound selector as we are acquiring them.
    Once we are done collecting the data for a compound selector, we freeze
    the data in an object that can be pickled and hashed.
    """

    def __init__(self, **kwargs: Any) -> None:
        """Initialize."""

        self.tag: ct.SelectorTag | None = kwargs.get('tag', None)
        self.ids: list[str] = kwargs.get('ids', [])
        self.classes: list[str] = kwargs.get('classes', [])
        self.attributes: list[ct.SelectorAttribute] = kwargs.get('attributes', [])
        self.nth: list[ct.SelectorNth] = kwargs.get('nth', [])
        self.selectors: list[ct.SelectorList] = kwargs.get('selectors', [])
        self.relations: list[_Selector] = kwargs.get('relations', [])
        self.rel_type: str | None = kwargs.get('rel_type', None)
        self.contains: list[ct.SelectorContains] = kwargs.get('contains', [])
        self.lang: list[ct.SelectorLang] = kwargs.get('lang', [])
        self.flags: int = kwargs.get('flags', 0)
        self.no_match: bool = kwargs.get('no_match', False)

    def _freeze(self, relation: ct.SelectorList) -> ct.Selector | ct.SelectorNull:
        """Freeze self with an already frozen relation."""

        if self.no_match:
            return ct.Null
        else:
            return ct.Selector(
                self.tag,
                tuple(self.ids),
                tuple(self.classes),
                tuple(self.attributes),
                tuple(self.nth),
                tuple(self.selectors),
                relation,
                self.rel_type,
                tuple(self.contains),
                tuple(self.lang),
                self.flags
            )

    def freeze(self) -> ct.Selector | ct.SelectorNull:
        """
        Freeze self.

        Relations form a chain where each selector's first relation is the next link and any
        remaining relations are carried down to follow it. As the far end of the chain must be
        frozen first, flatten the chain and then freeze it in reverse to avoid recursion.
        """

        chain: list[_Selector] = []
        pending = [self]
        while pending:
            sel = pending.pop()
            chain.append(sel)
            if sel.no_match:
                break
            pending.extend(reversed(sel.relations))

        relation = ct.SelectorList()
        for sel in reversed(chain):
            frozen = sel._freeze(relation)
            relation = ct.SelectorList([frozen])
        return frozen

    def __str__(self) -> str:  # pragma: no cover
        """String representation."""

        return (
            f'_Selector(tag={self.tag!r}, ids={self.ids!r}, classes={self.classes!r}, attributes={self.attributes!r}, '
            f'nth={self.nth!r}, selectors={self.selectors!r}, relations={self.relations!r}, '
            f'rel_type={self.rel_type!r}, contains={self.contains!r}, lang={self.lang!r}, flags={self.flags!r}, '
            f'no_match={self.no_match!r})'
        )

    __repr__ = __str__


class _SelectorContext:
    """
    Parsing state for a selector list.

    Context are created for each nesting level to avoid recursion.
    """

    def __init__(
        self,
        flags: int,
        index: int,
        count: int,
        on_close: Callable[[ct.SelectorList], None] | None = None
    ) -> None:
        """Initialize."""

        self.sel = _Selector()
        self.selectors: list[_Selector] = []
        self.relations: list[_Selector] = []
        self.has_selector = False
        self.rel_type = ":" + WS_COMBINATOR
        self.index = index
        self.count = count
        # Receives the frozen selector list when an open pseudo-class is closed.
        self.on_close = on_close

        # Setup various flags
        self.is_open = bool(flags & FLG_OPEN)
        self.is_pseudo = bool(flags & FLG_PSEUDO)
        self.is_relative = bool(flags & FLG_RELATIVE)
        self.is_not = bool(flags & FLG_NOT)
        self.is_html = bool(flags & FLG_HTML)
        self.is_default = bool(flags & FLG_DEFAULT)
        self.is_indeterminate = bool(flags & FLG_INDETERMINATE)
        self.is_in_range = bool(flags & FLG_IN_RANGE)
        self.is_out_of_range = bool(flags & FLG_OUT_OF_RANGE)
        self.is_placeholder_shown = bool(flags & FLG_PLACEHOLDER_SHOWN)
        self.is_forgive = bool(flags & FLG_FORGIVE)
        self.is_editable = bool(flags & FLG_EDITABLE)
        self.is_checked = bool(flags & FLG_CHECKED)

        # The algorithm for relative selectors require an initial selector in the selector list
        if self.is_relative:
            self.selectors.append(_Selector())

    def debug(self) -> None:  # pragma: no cover
        """Print out useful debug stuff."""

        for name in (
            'is_pseudo', 'is_open', 'is_relative', 'is_not', 'is_html', 'is_default', 'is_indeterminate',
            'is_in_range', 'is_out_of_range', 'is_placeholder_shown', 'is_forgive', 'is_editable', 'is_checked'
        ):
            if getattr(self, name):
                print(f'    {name}: True')


@dataclass
class CSSPattern:
    """A CSS pattern that hasn't been processed by `CSSParser` yet."""

    selector: str
    flags: int


class PseudoSelectorMap(UserDict[str, CSSPattern | ct.SelectorList]):
    """Pseudo selector map."""

    def __setitem__(self, key: str, value: CSSPattern | ct.SelectorList) -> None:
        """Set item."""

        self.data[key] = value

    def __getitem__(self, key: str) -> ct.SelectorList:
        """Get item."""

        with SEL_LOCK:
            value = self.data[key]
            if isinstance(value, CSSPattern):
                value = CSSParser(value.selector).process_selectors(flags=value.flags)
                self.data[key] = value

        return value


# An `input` is in the Text state when its `type` is missing, `text`, or not one of
# the other keywords, so an invalid value like `type="foo"` is a text input:
# https://html.spec.whatwg.org/multipage/input.html#attr-input-type
INPUT_TEXT_STATE = (
    ':not([type=hidden], [type=search], [type=tel], [type=url], [type=email], [type=password], '
    '[type=date], [type=month], [type=week], [type=time], [type=datetime-local], [type=number], '
    '[type=range], [type=color], [type=checkbox], [type=radio], [type=file], [type=submit], '
    '[type=image], [type=reset], [type=button])'
)

# CSS pattern for `:link` and `:any-link`
CSS_LINK = CSSPattern('html|*:is(a, area)[href]', FLG_PSEUDO | FLG_HTML)
# CSS pattern for `:checked`
CSS_CHECKED = CSSPattern(
    '''
    html|input[type=checkbox][checked],

    /*
    This pattern must be at the end.
    Special logic is applied to the last selector.
    */
    html|*:is(input[type=radio][checked], option)
    ''',
    FLG_PSEUDO | FLG_HTML | FLG_CHECKED
)
# CSS pattern for `:default`
CSS_DEFAULT = CSSPattern(
    '''
    html|*:is(input[type=checkbox], input[type=radio])[checked],
    html|option[selected],

    /*
    This pattern must be at the end.
    Special logic is applied to the last selector.
    */
    html|*:is(
        button:is([type=submit], :not([type=button], [type=reset], [commandfor], [command])),
        input:is([type=submit], [type=image])
    )
    ''',
    FLG_PSEUDO | FLG_HTML | FLG_DEFAULT
)
# CSS pattern for `:indeterminate`
CSS_INDETERMINATE = CSSPattern(
    '''
    html|input[type="checkbox"][indeterminate],
    html|input[type="radio"]:is(:not([name]), [name=""]):not([checked]),
    html|progress:not([value]),

    /*
    This pattern must be at the end.
    Special logic is applied to the last selector.
    */
    html|input[type="radio"][name]:not([name='']):not([checked])
    ''',
    FLG_PSEUDO | FLG_HTML | FLG_INDETERMINATE
)
# CSS pattern for `:disabled`
CSS_DISABLED = CSSPattern(
    '''
    html|*:is(input, button, select, textarea, fieldset, optgroup, option, fieldset)[disabled],
    html|optgroup[disabled] > html|option,
    html|fieldset[disabled] > html|*:is(input, button, select, textarea, fieldset),
    html|fieldset[disabled] >
        html|*:not(legend:nth-of-type(1)) html|*:is(input, button, select, textarea, fieldset),
    html|*:is(
        html|select[disabled],
        html|fieldset[disabled] > html|select,
        html|fieldset[disabled] > html|*:not(legend:nth-of-type(1)) html|select
    ) html|*:is(optgroup, option)
    ''',
    FLG_PSEUDO | FLG_HTML
)
# CSS pattern for `:enabled`
CSS_ENABLED = CSSPattern(
    '''
    html|*:is(input, button, select, textarea, fieldset, optgroup, option, fieldset):not(:disabled)
    ''',
    FLG_PSEUDO | FLG_HTML
)
# CSS pattern for `:required`
# The `required` attribute does not apply to `input` elements of these types.
INPUT_REQUIRED_NOT_APPLIES = (
    '[type=hidden], [type=range], [type=color], [type=submit], [type=image], [type=reset], [type=button]'
)
CSS_REQUIRED = CSSPattern(
    f'''
    html|input:not({INPUT_REQUIRED_NOT_APPLIES})[required],
    html|*:is(textarea, select)[required]
    ''',
    FLG_PSEUDO | FLG_HTML
)
# CSS pattern for `:optional`
CSS_OPTIONAL = CSSPattern(
    f'''
    html|*:is(input, textarea, select):not([required]),
    html|input:is({INPUT_REQUIRED_NOT_APPLIES})
    ''',
    FLG_PSEUDO | FLG_HTML
)
# CSS pattern for `:placeholder-shown`
CSS_PLACEHOLDER_SHOWN = CSSPattern(
    f'''
    html|*:is(
        input:is(
            {INPUT_TEXT_STATE},
            [type=search],
            [type=url],
            [type=tel],
            [type=email],
            [type=password],
            [type=number]
        ),
        textarea
    )[placeholder]:not([placeholder=''])
    ''',
    FLG_PSEUDO | FLG_HTML | FLG_PLACEHOLDER_SHOWN
)
# CSS pattern for `:read-write` (CSS_DISABLED must be compiled first)
CSS_READ_WRITE = CSSPattern(
    f'''
    html|*:is(
        textarea,
        input:is(
            {INPUT_TEXT_STATE},
            [type=search],
            [type=url],
            [type=tel],
            [type=email],
            [type=number],
            [type=password],
            [type=date],
            [type=datetime-local],
            [type=month],
            [type=time],
            [type=week]
        )
    ):not([readonly], :disabled),
    html|*:not(input, textarea, [contenteditable="false" i])
    ''',
    FLG_PSEUDO | FLG_HTML | FLG_EDITABLE
)
# CSS pattern for `:read-only`
CSS_READ_ONLY = CSSPattern('html|*:not(:read-write)', FLG_PSEUDO | FLG_HTML)
# `:in-range` and `:out-of-range` only consider an input that has range limitations and is a candidate
# for constraint validation. See `CSSMatch.match_range()` for the full rules and where they follow
# browsers instead of the letter of the HTML spec.
#
# - A `range` input always has range limitations: its minimum and maximum default to 0 and 100.
#   The other types need a `min` or `max`; `match_range()` rejects one that does not parse.
# - An input that is disabled (including through a disabled `fieldset`, outside its first `legend`),
#   `readonly`, or inside a `datalist` is barred from constraint validation and matches neither.
#   The spec says `readonly` does not apply to `range` and so does not bar it, but all browsers
#   treat a `readonly` `range` input as barred, so we do too.
RANGE_INPUT = """
    html|input:is(
        :is(
            [type="date"],
            [type="month"],
            [type="week"],
            [type="time"],
            [type="datetime-local"],
            [type="number"]
        ):is(
            [min],
            [max]
        ),
        [type="range"]
    ):not(:disabled, [readonly], html|datalist html|*)
"""
# CSS pattern for `:in-range`
CSS_IN_RANGE = CSSPattern(RANGE_INPUT, FLG_PSEUDO | FLG_HTML | FLG_IN_RANGE)
# CSS pattern for `:out-of-range`
CSS_OUT_OF_RANGE = CSSPattern(RANGE_INPUT, FLG_PSEUDO | FLG_HTML | FLG_OUT_OF_RANGE)
# CSS pattern for :open
CSS_OPEN = CSSPattern('html|*:is(details, dialog)[open]', FLG_PSEUDO | FLG_HTML)
# CSS pattern for :muted
CSS_MUTED = CSSPattern('html|*:is(video, audio)[muted]', FLG_PSEUDO | FLG_HTML)
# CSS pattern default for `:nth-child` "of S" feature
CSS_NTH_OF_S_DEFAULT = CSSPattern("*|*", FLG_PSEUDO)


class CSSParser:
    """Parse CSS selectors."""

    CSS_TOKENS = (
        SelectorPattern("pseudo_close", PAT_PSEUDO_CLOSE),
        SpecialPseudoPattern(
            (
                (
                    "pseudo_contains",
                    (':contains', ':-soup-contains', ':-soup-contains-own'),
                    PAT_PSEUDO_CONTAINS,
                    SelectorPattern
                ),
                ("pseudo_nth_child", (':nth-child', ':nth-last-child'), PAT_PSEUDO_NTH_CHILD, SelectorPattern),
                ("pseudo_nth_type", (':nth-of-type', ':nth-last-of-type'), PAT_PSEUDO_NTH_TYPE, SelectorPattern),
                ("pseudo_lang", (':lang',), PAT_PSEUDO_LANG, SelectorPattern),
                ("pseudo_dir", (':dir',), PAT_PSEUDO_DIR, SelectorPattern)
            )
        ),
        SelectorPattern("pseudo_class_custom", PAT_PSEUDO_CLASS_CUSTOM),
        SelectorPattern("pseudo_class", PAT_PSEUDO_CLASS),
        SelectorPattern("pseudo_element", PAT_PSEUDO_ELEMENT),
        SelectorPattern("amp", PAT_AMP),
        SelectorPattern("at_rule", PAT_AT_RULE),
        SelectorPattern("id", PAT_ID),
        SelectorPattern("class", PAT_CLASS),
        SelectorPattern("tag", PAT_TAG),
        SelectorPattern("attribute", PAT_ATTR),
        SelectorPatternCombinator("combine", PAT_COMBINE)
    )

    # Pseudos that expand to selectors
    PSEUDO_SELECTORS = PseudoSelectorMap(
        {
            ':link': CSS_LINK,
            ':any-link': CSS_LINK,
            ':checked': CSS_CHECKED,
            ':default': CSS_DEFAULT,
            ':indeterminate': CSS_INDETERMINATE,
            ':disabled': CSS_DISABLED,
            ':enabled': CSS_ENABLED,
            ':required': CSS_REQUIRED,
            ':muted': CSS_MUTED,
            ':open': CSS_OPEN,
            ':optional': CSS_OPTIONAL,
            ':read-only': CSS_READ_ONLY,
            ':read-write': CSS_READ_WRITE,
            ':in-range': CSS_IN_RANGE,
            ':out-of-range': CSS_OUT_OF_RANGE,
            ':placeholder-shown': CSS_PLACEHOLDER_SHOWN,
            '<nth-of-s>': CSS_NTH_OF_S_DEFAULT
        }
    )

    def __init__(
        self,
        selector: str,
        *,
        custom: Mapping[str, ct.SelectorList] | None = None,
        ignore: Iterable[str] | None = None,
        flags: int = 0,
        max_selectors: int = SELECTOR_LIMIT
    ) -> None:
        """Initialize."""

        self.pattern = selector.replace('\x00', '\ufffd')
        self.flags = flags
        self.debug = self.flags & util.DEBUG
        self.nostrict = self.flags & util.NOSTRICT
        self.custom = custom
        self.ignore = frozenset([] if ignore is None else ignore)
        self.count = 0
        self.maxsel = max_selectors

    def increment_count(self, increment: int = 1) -> None:
        """Check the current selector count."""

        self.count += increment
        if self.count > self.maxsel:
            raise ValueError(f'Selector exceeds pseudo-class nesting limit of {self.maxsel}')

    def parse_attribute_selector(self, sel: _Selector, m: Match[str], has_selector: bool) -> bool:
        """Create attribute selector from the returned regex match."""

        op = m.group('cmp')
        case = util.lower(m.group('case')) if m.group('case') else None
        ns = css_unescape(m.group('attr_ns')[:-1]) if m.group('attr_ns') else ''
        attr = css_unescape(m.group('attr_name'))
        html_insensitive = False
        pattern = None
        pattern2 = None
        inverse = False

        if case:
            ignore_case = case == 'i'
        elif util.lower(attr) in HTML_CASE_INSENSITIVE_ATTRIBUTES:
            ignore_case = True
            html_insensitive = True
        else:
            ignore_case = False

        if op:
            value = css_unescape(m.group('value'))
            pattern = self.parse_attribute_pattern(op, util.lower(value) if ignore_case else value)
            if html_insensitive:
                # Outside HTML, the value is matched case sensitive.
                pattern2 = self.parse_attribute_pattern(op, value)
            inverse = op.startswith('!')

        # Append the attribute selector
        sel_attr = ct.SelectorAttribute(attr, ns, pattern, pattern2, inverse, ignore_case)
        sel.attributes.append(sel_attr)

        has_selector = True
        return has_selector

    @staticmethod
    def parse_attribute_pattern(op: str, value: str) -> re.Pattern[str]:
        """
        Create the pattern for an attribute selector's value.

        The pattern is applied with `search()`, so it must not need a leading `.*?` to find the value, which would make
        the regular expression engine retry the value at every position instead of scanning for it as a literal.

        A case insensitive pattern is compiled case sensitive for the value converted to ASCII lowercase and is matched
        against the attribute value converted the same way. We purposely avoid using `re.I` as the regular expression
        engine cannot then scan as a literal, making the search take quadratic time depending on the pattern.
        """

        if op.startswith('^'):
            # Value start with
            # `^=` should match nothing if the value is empty, so use `(?!)` which cannot be matched.
            source = r'^(?!)' if not value else fr'^{re.escape(value)}'
        elif op.startswith('$'):
            # Value ends with
            # `$=` should match nothing if the value is empty, so use `(?!)` which cannot be matched.
            source = r'(?!)' if not value else fr'{re.escape(value)}\Z'
        elif op.startswith('*'):
            # Value contains
            # `*=` should match nothing if the value is empty, so use `(?!)` which cannot be matched.
            source = r'(?!)' if not value else re.escape(value)
        elif op.startswith('~'):
            # Value contains word within space separated list
            # `~=` should match nothing if the value is empty, so use `(?!)` which cannot be matched.
            value = r'(?!)' if not value or RE_WS.search(value) else re.escape(value)
            source = fr'(?:(?<=^)|(?<=[ \t\r\n\f])){value}(?=(?:[ \t\r\n\f]|\Z))'
        elif op.startswith('|'):
            # Value starts with word in dash separated list
            source = fr'^{re.escape(value)}(?:-.*)?\Z'
        elif op.startswith('!'):
            # Value does not matches
            source = fr'^(?!{re.escape(value)}\Z)'
        else:
            # Value matches
            source = fr'^{re.escape(value)}\Z'
        return re.compile(source, re.DOTALL)

    def parse_tag_pattern(self, sel: _Selector, m: Match[str], has_selector: bool) -> bool:
        """Parse tag pattern from regex match."""

        prefix = css_unescape(m.group('tag_ns')[:-1]) if m.group('tag_ns') else None
        tag = css_unescape(m.group('tag_name'))
        sel.tag = ct.SelectorTag(tag, prefix)
        has_selector = True
        return has_selector

    def parse_pseudo_class_custom(self, sel: _Selector, m: Match[str], has_selector: bool) -> bool:
        """
        Parse custom pseudo class alias.

        Compile custom selectors as we need them. When compiling a custom selector,
        set it to `None` in the dictionary so we can avoid an infinite loop.
        """

        pseudo = util.lower(css_unescape(m.group('name')))
        selector = self.custom.get(pseudo) if self.custom is not None else None
        if selector is None:
            raise SelectorSyntaxError(
                f"Undefined custom selector '{pseudo}' found at position {m.end(0)}",
                self.pattern,
                m.end(0)
            )

        self.increment_count(selector.count)
        sel.selectors.append(selector)
        has_selector = True
        return has_selector

    def parse_pseudo_class(
        self,
        sel: _Selector,
        m: Match[str],
        has_selector: bool,
        is_relative: bool
    ) -> tuple[bool, _SelectorContext | None]:
        """
        Parse pseudo class.

        If the pseudo-class opens a selector list, a new context is returned to parse its contents.
        """

        context = None
        complex_pseudo = False
        pseudo = util.lower(css_unescape(m.group('name')))
        if m.group('open'):
            complex_pseudo = True
        if complex_pseudo and pseudo in PSEUDO_COMPLEX:
            context = self.parse_pseudo_open(pseudo, m.end(0), sel.selectors.append, is_relative)
            has_selector = True
        elif not complex_pseudo and pseudo in PSEUDO_SIMPLE:
            if pseudo == ':root':
                sel.flags |= ct.SEL_ROOT
            elif pseudo == ':defined':
                sel.flags |= ct.SEL_DEFINED
            elif pseudo == ':scope':
                sel.flags |= ct.SEL_SCOPE
            elif pseudo == ':empty':
                sel.flags |= ct.SEL_EMPTY
            elif pseudo in self.PSEUDO_SELECTORS:
                pseudo_selector = self.PSEUDO_SELECTORS[pseudo]
                self.increment_count(pseudo_selector.count)
                sel.selectors.append(pseudo_selector)
            elif pseudo == ':first-child':
                sel.nth.append(ct.SelectorNth(0, False, 1, False, False, ct.SelectorList()))
            elif pseudo == ':last-child':
                sel.nth.append(ct.SelectorNth(0, False, 1, False, True, ct.SelectorList()))
            elif pseudo == ':first-of-type':
                sel.nth.append(ct.SelectorNth(0, False, 1, True, False, ct.SelectorList()))
            elif pseudo == ':last-of-type':
                sel.nth.append(ct.SelectorNth(0, False, 1, True, True, ct.SelectorList()))
            elif pseudo == ':only-child':
                sel.nth.extend(
                    [
                        ct.SelectorNth(0, False, 1, False, False, ct.SelectorList()),
                        ct.SelectorNth(0, False, 1, False, True, ct.SelectorList())
                    ]
                )
            elif pseudo == ':only-of-type':
                sel.nth.extend(
                    [
                        ct.SelectorNth(0, False, 1, True, False, ct.SelectorList()),
                        ct.SelectorNth(0, False, 1, True, True, ct.SelectorList())
                    ]
                )
            has_selector = True
        elif complex_pseudo and pseudo in PSEUDO_COMPLEX_NO_MATCH:
            context = self.parse_pseudo_open(pseudo, m.end(0))
            sel.no_match = True
            has_selector = True
        elif not complex_pseudo and pseudo in PSEUDO_SIMPLE_NO_MATCH:
            sel.no_match = True
            has_selector = True
        elif pseudo in PSEUDO_SUPPORTED:
            raise SelectorSyntaxError(
                f"Invalid syntax for pseudo class '{pseudo}'",
                self.pattern,
                m.start(0)
            )
        else:
            raise SelectorSyntaxError(
                f"'{pseudo}' was detected as a pseudo-class and is either unsupported or invalid. "
                "If the syntax was not intended to be recognized as a pseudo-class, please escape the colon.",
                self.pattern,
                m.start(0)
            )

        return has_selector, context

    def parse_pseudo_nth(
        self,
        sel: _Selector,
        m: Match[str],
        has_selector: bool
    ) -> tuple[bool, _SelectorContext | None]:
        """
        Parse `nth` pseudo.

        If `of S` is specified, a new context is returned to parse `S`.
        """

        context = None
        mdict = m.groupdict()
        if mdict.get('pseudo_nth_child'):
            postfix = '_child'
        else:
            postfix = '_type'
        mdict['name'] = util.lower(css_unescape(mdict['name']))
        content = util.lower(mdict.get('nth' + postfix))
        if content == 'even':
            # 2n
            s1 = 2
            s2 = 0
            var = True
        elif content == 'odd':
            # 2n+1
            s1 = 2
            s2 = 1
            var = True
        else:
            nth_parts = cast(Match[str], RE_NTH.match(content))
            _s1 = '-' if nth_parts.group('s1') and nth_parts.group('s1') == '-' else ''
            _s2 = ''
            a = nth_parts.group('a')
            var = a.endswith('n')
            if a.startswith('n'):
                _s1 += '1'
            elif var:
                _s1 += a[:-1]
            else:
                _s2 = _s1 + a
                _s1 = ''
            if nth_parts.group('s2') and nth_parts.group('s2') == '-':
                _s2 = '-'
            if nth_parts.group('b'):
                _s2 += nth_parts.group('b')
            s1 = int(_s1, 10) if _s1 else 0
            s2 = int(_s2, 10) if _s2 else 0

        pseudo_sel = mdict['name']
        if postfix == '_child':
            last = pseudo_sel == ':nth-last-child'

            def append_nth(nth_sel: ct.SelectorList) -> None:
                """Append to this selector's `nth` attribute."""

                sel.nth.append(ct.SelectorNth(s1, var, s2, False, last, nth_sel))

            if m.group('of'):
                # Parse the rest of `of S`.
                context = self.parse_pseudo_open(pseudo_sel, m.end(0), append_nth)
            else:
                # Use default `*|*` for `of S`.
                nth_sel = self.PSEUDO_SELECTORS['<nth-of-s>']
                self.increment_count(nth_sel.count)
                append_nth(nth_sel)
        else:
            if pseudo_sel == ':nth-of-type':
                sel.nth.append(ct.SelectorNth(s1, var, s2, True, False, ct.SelectorList()))
            elif pseudo_sel == ':nth-last-of-type':
                sel.nth.append(ct.SelectorNth(s1, var, s2, True, True, ct.SelectorList()))
        has_selector = True
        return has_selector, context

    def parse_pseudo_open(
        self,
        name: str,
        index: int,
        on_close: Callable[[ct.SelectorList], None] | None = None,
        is_relative: bool = False
    ) -> _SelectorContext:
        """Create a new context for nested pseudo-class selectors."""

        flags = FLG_PSEUDO | FLG_OPEN
        if name == ':not':
            flags |= FLG_NOT
        elif name == ':has':
            if is_relative and not self.nostrict:
                raise SelectorSyntaxError(
                    f":has() was found to be nested under :has() at position {index}",
                    self.pattern,
                    index
                )
            flags |= FLG_RELATIVE
        elif name in (':where', ':is'):
            flags |= FLG_FORGIVE

        return self.create_context(flags, index, on_close)

    def create_context(
        self,
        flags: int,
        index: int,
        on_close: Callable[[ct.SelectorList], None] | None = None
    ) -> _SelectorContext:
        """Create a new selector context."""

        context = _SelectorContext(flags, index, self.count, on_close)
        if self.debug:  # pragma: no cover
            context.debug()
        return context

    def parse_has_combinator(
        self,
        sel: _Selector,
        m: Match[str],
        has_selector: bool,
        selectors: list[_Selector],
        rel_type: str,
        index: int
    ) -> tuple[bool, _Selector, str]:
        """Parse combinator tokens."""

        combinator = m.group('relation')
        if not combinator:
            combinator = WS_COMBINATOR
        if combinator == COMMA_COMBINATOR:
            if not has_selector:
                raise SelectorSyntaxError(
                    f"The combinator '{combinator}' at position {index}, must have a selector before it",
                    self.pattern,
                    index
                )
            sel.rel_type = rel_type
            selectors[-1].relations.append(sel)
            rel_type = ":" + WS_COMBINATOR
            selectors.append(_Selector())
        else:
            if has_selector and self.nostrict:
                # End the current selector and associate the leading combinator with this selector.
                sel.rel_type = rel_type
                selectors[-1].relations.append(sel)
            elif has_selector:
                raise SelectorSyntaxError(
                    f'Using strict rules as proposed by CSS, the complex selectors at postition {index} is not allowed',
                    self.pattern,
                    index
                )
            elif rel_type[1:] != WS_COMBINATOR:
                # It's impossible to have two whitespace combinators after each other as the patterns
                # will gobble up trailing whitespace. It is also impossible to have a whitespace
                # combinator after any other kind for the same reason. But we could have
                # multiple non-whitespace combinators. So if the current combinator is not a whitespace,
                # then we've hit the multiple combinator case, so we should fail.
                raise SelectorSyntaxError(
                    f'The multiple combinators at position {index}',
                    self.pattern,
                    index
                )

            # Set the leading combinator for the next selector.
            rel_type = ':' + combinator

        sel = _Selector()
        has_selector = False
        return has_selector, sel, rel_type

    def parse_combinator(
        self,
        sel: _Selector,
        m: Match[str],
        has_selector: bool,
        selectors: list[_Selector],
        relations: list[_Selector],
        is_pseudo: bool,
        is_forgive: bool,
        index: int
    ) -> tuple[bool, _Selector]:
        """Parse combinator tokens."""

        combinator = m.group('relation')
        if not combinator:
            combinator = WS_COMBINATOR
        if not has_selector:
            if not is_forgive or combinator != COMMA_COMBINATOR:
                raise SelectorSyntaxError(
                    f"The combinator '{combinator}' at position {index}, must have a selector before it",
                    self.pattern,
                    index
                )

            # If we are in a forgiving pseudo class, just make the selector a "no match"
            if combinator == COMMA_COMBINATOR:
                self.increment_count()
                del relations[:]
        else:
            if combinator == COMMA_COMBINATOR:
                if not sel.tag and not is_pseudo:
                    # Implied `*`
                    sel.tag = ct.SelectorTag('*', None)
                sel.relations.extend(relations)
                selectors.append(sel)
                del relations[:]
            else:
                sel.relations.extend(relations)
                sel.rel_type = combinator
                del relations[:]
                relations.append(sel)

        sel = _Selector()
        has_selector = False

        return has_selector, sel

    def parse_class_id(self, sel: _Selector, m: Match[str], has_selector: bool) -> bool:
        """Parse HTML classes and ids."""

        selector = m.group(0)
        if selector.startswith('.'):
            sel.classes.append(css_unescape(selector[1:]))
        else:
            sel.ids.append(css_unescape(selector[1:]))
        has_selector = True
        return has_selector

    def parse_pseudo_contains(self, sel: _Selector, m: Match[str], has_selector: bool) -> bool:
        """Parse contains."""

        pseudo = util.lower(css_unescape(m.group('name')))
        if pseudo == ":contains":
            warnings.warn(  # noqa: B028
                "The pseudo class ':contains' is deprecated, ':-soup-contains' should be used moving forward.",
                FutureWarning
            )
        contains_own = pseudo == ":-soup-contains-own"
        patterns = [
            css_unescape(token.group('value'))
            for token in RE_VALUES.finditer(m.group('values'))
            if not token.group('split')
        ]
        sel.contains.append(ct.SelectorContains(patterns, contains_own))
        has_selector = True
        return has_selector

    def parse_pseudo_lang(self, sel: _Selector, m: Match[str], has_selector: bool) -> bool:
        """Parse pseudo language."""

        patterns = [
            css_unescape(token.group('value'))
            for token in RE_VALUES.finditer(m.group('values'))
            if not token.group('split')
        ]
        sel.lang.append(ct.SelectorLang(patterns))
        has_selector = True

        return has_selector

    def parse_pseudo_dir(self, sel: _Selector, m: Match[str], has_selector: bool) -> bool:
        """Parse pseudo direction."""

        value = util.lower(css_unescape(m.group('dir')))
        if value == 'ltr':
            sel.flags |= ct.SEL_DIR_LTR
        elif value == 'rtl':
            sel.flags |= ct.SEL_DIR_RTL
        else:
            # Any other identifier is valid, but does not match anything.
            sel.no_match = True
        has_selector = True
        return has_selector

    def parse_selectors(
        self,
        iselector: Iterator[tuple[str, Match[str]]],
        index: int = 0,
        flags: int = 0
    ) -> ct.SelectorList:
        """
        Parse selectors.

        Pseudo-classes that open a nested selector list push a new context onto a stack,
        and closing them pops back to the parent context, so nesting does not recurse.
        """

        ctx = self.create_context(flags, index)
        stack: list[_SelectorContext] = []

        for key, m in iselector:
            child: _SelectorContext | None = None

            if key not in ('combine', 'pseudo_close'):
                self.increment_count()

            # Handle parts
            if self.ignore:
                mi = RE_PSEUDO_IGNORE.match(m.group(0))
                if mi is not None and mi.group('name').lower() in self.ignore:
                    raise SelectorSyntaxError(
                        f"The selector '{mi.group('name')}' at position {m.start(0)}, has been disallowed",
                        self.pattern,
                        m.start(0)
                    )
            if key == "at_rule":
                raise NotImplementedError(f"At-rules found at position {m.start(0)}")
            elif key == "amp":
                ctx.sel.flags |= ct.SEL_SCOPE
                ctx.has_selector = True
            elif key == 'pseudo_class_custom':
                ctx.has_selector = self.parse_pseudo_class_custom(ctx.sel, m, ctx.has_selector)
            elif key == 'pseudo_class':
                ctx.has_selector, child = self.parse_pseudo_class(ctx.sel, m, ctx.has_selector, ctx.is_relative)
            elif key == 'pseudo_element':
                raise NotImplementedError(f"Pseudo-element found at position {m.start(0)}")
            elif key == 'pseudo_contains':
                ctx.has_selector = self.parse_pseudo_contains(ctx.sel, m, ctx.has_selector)
            elif key in ('pseudo_nth_type', 'pseudo_nth_child'):
                ctx.has_selector, child = self.parse_pseudo_nth(ctx.sel, m, ctx.has_selector)
            elif key == 'pseudo_lang':
                ctx.has_selector = self.parse_pseudo_lang(ctx.sel, m, ctx.has_selector)
            elif key == 'pseudo_dir':
                ctx.has_selector = self.parse_pseudo_dir(ctx.sel, m, ctx.has_selector)
            elif key == 'pseudo_close':
                if not ctx.has_selector:
                    if not ctx.is_forgive:
                        raise SelectorSyntaxError(
                            f"Expected a selector at position {m.start(0)}",
                            self.pattern,
                            m.start(0)
                        )
                    ctx.sel.no_match = True
                if not ctx.is_open:
                    raise SelectorSyntaxError(
                        f"Unmatched pseudo-class close at position {m.start(0)}",
                        self.pattern,
                        m.start(0)
                    )

                # Hand the completed selector list to the parent and resume parsing the parent.
                selectors = self.finalize_selectors(ctx)
                if ctx.on_close is not None:
                    ctx.on_close(selectors)
                ctx = stack.pop()
                continue
            elif key == 'combine':
                if ctx.is_relative:
                    ctx.has_selector, ctx.sel, ctx.rel_type = self.parse_has_combinator(
                        ctx.sel, m, ctx.has_selector, ctx.selectors, ctx.rel_type, ctx.index
                    )
                else:
                    ctx.has_selector, ctx.sel = self.parse_combinator(
                        ctx.sel, m, ctx.has_selector, ctx.selectors, ctx.relations,
                        ctx.is_pseudo, ctx.is_forgive, ctx.index
                    )
            elif key == 'attribute':
                ctx.has_selector = self.parse_attribute_selector(ctx.sel, m, ctx.has_selector)
            elif key == 'tag':
                if ctx.has_selector:
                    raise SelectorSyntaxError(
                        f"Tag name found at position {m.start(0)} instead of at the start",
                        self.pattern,
                        m.start(0)
                    )
                ctx.has_selector = self.parse_tag_pattern(ctx.sel, m, ctx.has_selector)
            elif key in ('class', 'id'):
                ctx.has_selector = self.parse_class_id(ctx.sel, m, ctx.has_selector)

            ctx.index = m.end(0)

            # A pseudo-class opened a nested selector list, parse it before resuming this one.
            if child is not None:
                stack.append(ctx)
                ctx = child

        # Handle selectors that are not closed
        if ctx.is_open:
            raise SelectorSyntaxError(
                f"Unclosed pseudo-class at position {ctx.index}",
                self.pattern,
                ctx.index
            )

        return self.finalize_selectors(ctx)

    def finalize_selectors(self, ctx: _SelectorContext) -> ct.SelectorList:
        """Complete the selector list of a context and freeze it."""

        sel = ctx.sel
        selectors = ctx.selectors
        relations = ctx.relations
        has_selector = ctx.has_selector

        # Cleanup completed selector piece
        if has_selector:
            if not sel.tag and not ctx.is_pseudo:
                # Implied `*`
                sel.tag = ct.SelectorTag('*', None)
            if ctx.is_relative:
                sel.rel_type = ctx.rel_type
                selectors[-1].relations.append(sel)
            else:
                sel.relations.extend(relations)
                del relations[:]
                selectors.append(sel)

        # Forgive empty slots in pseudo-classes that have lists (and are forgiving)
        elif ctx.is_forgive and (not selectors or not relations):
            # Handle normal pseudo-classes with empty slots like `:is()` etc.
            self.increment_count()
            sel.no_match = True
            del relations[:]
            selectors.append(sel)
            has_selector = True

        if not has_selector:
            # We will always need to finish a selector when `:has()` is used as it leads with combining.
            # May apply to others as well.
            raise SelectorSyntaxError(
                f'Expected a selector at position {ctx.index}',
                self.pattern,
                ctx.index
            )

        # Some patterns require additional logic, such as default. We try to make these the
        # last pattern, and append the appropriate flag to that selector which communicates
        # to the matcher what additional logic is required.
        if ctx.is_default:
            selectors[-1].flags = ct.SEL_DEFAULT
        if ctx.is_indeterminate:
            selectors[-1].flags = ct.SEL_INDETERMINATE
        if ctx.is_in_range:
            selectors[-1].flags = ct.SEL_IN_RANGE
        if ctx.is_out_of_range:
            selectors[-1].flags = ct.SEL_OUT_OF_RANGE
        if ctx.is_placeholder_shown:
            selectors[-1].flags = ct.SEL_PLACEHOLDER_SHOWN
        if ctx.is_editable:
            selectors[-1].flags = ct.SEL_EDITABLE
        if ctx.is_checked:
            selectors[-1].flags = ct.SEL_CHECKED

        # Return selector list
        return ct.SelectorList([s.freeze() for s in selectors], ctx.is_not, ctx.is_html, self.count - ctx.count)

    def selector_iter(self, pattern: str) -> Iterator[tuple[str, Match[str]]]:
        """Iterate selector tokens."""

        index = 0
        end = len(pattern) - 1

        if self.debug:  # pragma: no cover
            print(f'## PARSING: {pattern!r}')
        while index <= end:
            token = None
            for v in self.CSS_TOKENS:
                token = v.match(pattern, index, self.flags)
                if token is not None:
                    name, m = token
                    if self.debug:  # pragma: no cover
                        print(f"TOKEN: '{name or 'consumed'}' --> {m.group(0)!r} at position {m.start(0)}")
                    index = m.end(0)
                    if name is not None:
                        yield name, m
                    break
            if token is None:
                c = pattern[index]
                # If the character represents the start of one of the known selector types,
                # throw an exception mentioning that the known selector type is in error;
                # otherwise, report the invalid character.
                if c == '[':
                    msg = f"Malformed attribute selector at position {index}"
                elif c == '.':
                    msg = f"Malformed class selector at position {index}"
                elif c == '#':
                    msg = f"Malformed id selector at position {index}"
                elif c == ':':
                    msg = f"Malformed pseudo-class selector at position {index}"
                else:
                    msg = f"Invalid character {c!r} position {index}"
                raise SelectorSyntaxError(msg, self.pattern, index)
        if self.debug:  # pragma: no cover
            print('## END PARSING')

    def process_selectors(self, index: int = 0, flags: int = 0) -> ct.SelectorList:
        """Process selectors."""

        return self.parse_selectors(self.selector_iter(self.pattern), index, flags)
