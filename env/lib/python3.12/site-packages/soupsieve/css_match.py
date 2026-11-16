"""CSS matcher."""
from __future__ import annotations
from bisect import bisect_right
from datetime import datetime
from math import isfinite
from collections.abc import Hashable
from . import bidi
from . import util
import re
from . import css_types as ct
import bs4
from typing import (
    Generator,
    Iterator,
    Iterable,
    Any,
    Callable,
    Sequence,
    overload,
    Literal,
    NamedTuple,
    cast,
    TYPE_CHECKING
)  # noqa: F401, F811

# Empty tag pattern (whitespace okay)
RE_NOT_EMPTY = re.compile('[^ \t\r\n\f]')

RE_NOT_WS = re.compile('[^ \t\r\n\f]+')

# Relationships
REL_PARENT = ' '
REL_CLOSE_PARENT = '>'
REL_SIBLING = '~'
REL_CLOSE_SIBLING = '+'

# Relationships for :has() (forward looking)
REL_HAS_PARENT = ': '
REL_HAS_CLOSE_PARENT = ':>'
REL_HAS_SIBLING = ':~'
REL_HAS_CLOSE_SIBLING = ':+'

# `bs4` imports this module while it is still initializing,
# so these must only be evaluated when type checking.
if TYPE_CHECKING:  # pragma: no cover
    MatchRequest = tuple[bs4.Tag, ct.SelectorList]
    MatchGenerator = Generator[MatchRequest, bool, bool]

NS_XHTML = 'http://www.w3.org/1999/xhtml'
NS_XML = 'http://www.w3.org/XML/1998/namespace'
NS_SVG = 'http://www.w3.org/2000/svg'

# Values of `type` that put an `input` in a state that does not use its value for `dir="auto"`.
# A missing or invalid value is the Text state, which does.
NON_AUTO_DIR_INPUT_TYPES = frozenset((
    'date', 'month', 'week', 'time', 'datetime-local', 'number', 'range', 'color', 'checkbox',
    'radio', 'file', 'image'
))

# HTML elements whose text is ignored when resolving `dir="auto"` on an ancestor.
AUTO_DIR_IGNORED_ELEMENTS = frozenset(('bdi', 'script', 'style', 'textarea'))

DIR_FLAGS = ct.SEL_DIR_LTR | ct.SEL_DIR_RTL
RANGES = ct.SEL_IN_RANGE | ct.SEL_OUT_OF_RANGE

DIR_MAP = {
    'ltr': ct.SEL_DIR_LTR,
    'rtl': ct.SEL_DIR_RTL,
    'auto': 0
}

RE_NUM = re.compile(r"^(?P<value>-?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)\Z")
RE_TIME = re.compile(r'^(?P<hour>[0-9]{2}):(?P<minutes>[0-9]{2})(?::(?P<seconds>[0-9]{2}(?:\.[0-9]{1,3})?))?\Z')
RE_MONTH = re.compile(r'^(?P<year>[0-9]{4,})-(?P<month>[0-9]{2})\Z')
RE_WEEK = re.compile(r'^(?P<year>[0-9]{4,})-W(?P<week>[0-9]{2})\Z')
RE_DATE = re.compile(r'^(?P<year>[0-9]{4,})-(?P<month>[0-9]{2})-(?P<day>[0-9]{2})\Z')
RE_DATETIME = re.compile(
    r'^(?P<year>[0-9]{4,})-(?P<month>[0-9]{2})-(?P<day>[0-9]{2})[T ](?P<hour>[0-9]{2}):(?P<minutes>[0-9]{2})'
    r'(?::(?P<seconds>[0-9]{2}(?:\.[0-9]{1,3})?))?\Z'
)
RE_SIZE = re.compile(r'[ \t\r\n\f]*\+?(?P<value>[0-9]+)')
# BCP 47 syntax for an extended language range (RFC 4647) and a language tag (RFC 5646), as browsers check it:
# subtags of 1 to 8 ASCII letters and digits, the first only letters, and `*` subtags only in a range.
# Subtags are not checked against the registry.
RE_LANG_RANGE = re.compile(r'(?:[a-zA-Z]{1,8}|\*)(?:-(?:[a-zA-Z0-9]{1,8}|\*))*')
RE_LANG_TAG = re.compile(r'[a-zA-Z]{1,8}(?:-[a-zA-Z0-9]{1,8})*')

MONTHS_30 = (4, 6, 9, 11)  # April, June, September, and November
FEB = 2
SHORT_MONTH = 30
LONG_MONTH = 31
FEB_MONTH = 28
FEB_LEAP_MONTH = 29

# The latest value each date and time input type accepts in browsers. The HTML spec puts no upper
# limit on the year, but browsers store these values as a JavaScript time, which ends at
# 275760-09-13T00:00:00Z, and reject a value whose period starts after it. Chrome, Firefox and
# Safari agree on `date` and `datetime-local`; Chrome is the only one with `month` and `week`.
MAX_INPUT_VALUE: dict[str, tuple[float, ...]] = {
    'date': (275760, 9, 13),
    'month': (275760, 9),
    'week': (275760, 37),
    'datetime-local': (275760, 9, 13, 0, 0, 0),
}
DAYS_IN_WEEK = 7


class NthState:
    """Lazily built positions of the siblings counted by an `nth` selector under one parent."""

    __slots__ = ('pos', 'count', 'positions')

    def __init__(self, pos: int, count: int, positions: dict[int, int]) -> None:
        """Initialize."""

        # Next position in the parent's contents to scan
        self.pos = pos
        # Number of counted siblings found so far
        self.count = count
        # 1-based index of each counted sibling, keyed by `id()`
        self.positions = positions


class TreeForms(NamedTuple):
    """The form information of a tree. Elements are tracked by `id()` as `bs4` hashes them by their content."""

    # The form owner of each `input` and `button`
    owners: dict[int, bs4.Tag | None]
    # The default button of each form
    defaults: dict[int, bs4.Tag]
    # The last radio button with a `checked` attribute of each radio button group, by form owner and name
    checked: dict[tuple[int, str], bs4.Tag]


class _DocumentNav:
    """Navigate a Beautiful Soup document."""

    @classmethod
    def assert_valid_input(cls, tag: Any) -> None:
        """Check if valid input tag or document."""

        # Fail on unexpected types.
        if not cls.is_tag(tag):
            raise TypeError(f"Expected a BeautifulSoup 'Tag', but instead received type {type(tag)}")

    @staticmethod
    def is_doc(obj: bs4.element.PageElement | None) -> bool:
        """Is `BeautifulSoup` object."""
        return isinstance(obj, bs4.BeautifulSoup)

    @staticmethod
    def is_tag(obj: bs4.element.PageElement | None) -> bool:
        """Is tag."""
        return isinstance(obj, bs4.Tag)

    @staticmethod
    def is_declaration(obj: bs4.element.PageElement | None) -> bool:  # pragma: no cover
        """Is declaration."""
        return isinstance(obj, bs4.Declaration)

    @staticmethod
    def is_cdata(obj: bs4.element.PageElement | None) -> bool:
        """Is CDATA."""
        return isinstance(obj, bs4.CData)

    @staticmethod
    def is_processing_instruction(obj: bs4.element.PageElement | None) -> bool:  # pragma: no cover
        """Is processing instruction."""
        return isinstance(obj, bs4.ProcessingInstruction)

    @staticmethod
    def is_navigable_string(obj: bs4.element.PageElement | None) -> bool:
        """Is navigable string."""
        return isinstance(obj, bs4.element.NavigableString)

    @staticmethod
    def is_special_string(obj: bs4.element.PageElement | None) -> bool:
        """Is special string."""
        return isinstance(obj, (bs4.Comment, bs4.Declaration, bs4.CData, bs4.ProcessingInstruction, bs4.Doctype))

    @classmethod
    def is_content_string(cls, obj: bs4.element.PageElement | None) -> bool:
        """Check if node is content string."""

        return cls.is_navigable_string(obj) and not cls.is_special_string(obj)

    @staticmethod
    def is_xml_tree(el: bs4.Tag | None) -> bool:
        """Check if element (or document) is from a XML tree."""

        return el is not None and bool(el._is_xml)

    def is_template(self, el: bs4.Tag) -> bool:
        """
        Check if element is an HTML `template`.

        The contents of a `template` are not part of the document, so navigation stops at one in both directions.
        This is called for every element that is navigated, so avoid normalizing names that can't match.
        """

        if len(el.name) != 8:
            return False

        return bool(
            ((el.name if self.is_xml_tree(el) else util.lower(el.name)) == 'template') and
            self.is_html_tag(el)  # type: ignore[attr-defined]
        )

    def is_root(self, el: bs4.Tag) -> bool:
        """Return whether element is the root of the tree (which we have already pre-calculated)."""

        return bool(self.root and self.root is el)  # type: ignore[attr-defined]

    def has_contents(self, el: bs4.Tag | None, template_contents: bool) -> bool:
        """Check if the contents of an element should be navigated."""

        return el is not None and (template_contents or not self.is_template(el))

    def get_contents(
        self,
        el: bs4.Tag | None,
        template_contents: bool = False
    ) -> Iterator[bs4.element.PageElement]:
        """Get contents or contents in reverse."""

        if self.has_contents(el, template_contents):
            yield from cast('bs4.Tag', el).contents

    def get_tag_children(
        self,
        el: bs4.Tag | None,
        start: int | None = None,
        reverse: bool = False
    ) -> Iterator[bs4.Tag]:
        """Get tag children."""

        return self.get_children(el, start, reverse, True)

    @overload
    def get_children(
        self,
        el: bs4.Tag | None,
        start: int | None = None,
        reverse: bool = False,
        tags: Literal[True] = ...,
        template_contents: bool = False
    ) -> Iterator[bs4.Tag]:
        ...

    @overload
    def get_children(
        self,
        el: bs4.Tag | None,
        start: int | None = None,
        reverse: bool = False,
        tags: Literal[False] = ...,
        template_contents: bool = False
    ) -> Iterator[bs4.element.PageElement]:
        ...

    def get_children(
        self,
        el: bs4.Tag | None,
        start: int | None = None,
        reverse: bool = False,
        tags: Literal[True] | Literal[False] = False,
        template_contents: bool = False
    ) -> Iterator[bs4.element.PageElement]:
        """Get children."""

        if el is not None and self.has_contents(el, template_contents):
            last = len(el.contents) - 1
            if start is None:
                index = last if reverse else 0
            else:
                index = start
            end = -1 if reverse else last + 1
            incr = -1 if reverse else 1

            if 0 <= index <= last:
                for i in range(index, end, incr):
                    node = el.contents[i]
                    if not tags or self.is_tag(node):
                        yield node

    def get_tag_descendants(
        self,
        el: bs4.Tag | None,
        template_contents: bool = False
    ) -> Iterator[bs4.Tag]:
        """Specifically get tag descendants."""

        yield from self.get_descendants(  # type: ignore[misc]
            el, tags=True, template_contents=template_contents
        )

    def get_descendants(
        self,
        el: bs4.Tag | None,
        tags: bool = False,
        template_contents: bool = False
    ) -> Iterator[bs4.element.PageElement]:
        """
        Get descendants.

        `template_contents` only allows entering `el`, any `template` found within it is still not entered.
        """

        if el is not None and self.has_contents(el, template_contents):
            next_good = None
            for child in el.descendants:

                if next_good is not None:
                    if child is not next_good:
                        continue
                    next_good = None

                if isinstance(child, bs4.Tag):
                    if self.is_template(child):
                        if child.next_sibling is not None:
                            next_good = child.next_sibling
                        else:
                            last_child: bs4.element.PageElement = child
                            while isinstance(last_child, bs4.Tag) and last_child.contents:
                                last_child = last_child.contents[-1]
                            next_good = last_child.next_element
                        yield child
                        if next_good is None:
                            break
                        # Coverage isn't seeing this even though it's executed
                        continue  # pragma: no cover
                    yield child

                elif not tags:
                    yield child

    def get_parent(
        self,
        el: bs4.Tag | None,
        no_doc: bool = True,
        no_template: bool = True
    ) -> bs4.Tag | None:
        """Get parent."""

        parent = el.parent if el is not None else None
        if no_template and parent is not None and self.is_template(parent):
            parent = None
        if no_doc and self.is_doc(parent):
            parent = None
        return parent

    @staticmethod
    def get_tag_name(el: bs4.Tag | None) -> str | None:
        """Get tag."""

        return el.name if el is not None else None

    @staticmethod
    def get_prefix_name(el: bs4.Tag) -> str | None:
        """Get prefix."""

        return el.prefix

    @staticmethod
    def get_uri(el: bs4.Tag | None) -> str | None:
        """Get namespace `URI`."""

        return el.namespace if el is not None else None

    @classmethod
    def get_next_tag(cls, el: bs4.Tag) -> bs4.Tag | None:
        """Get next sibling tag."""

        return cls.get_next(el, tags=True)  # type: ignore[return-value]

    @classmethod
    def get_next(cls, el: bs4.Tag, tags: bool = False) -> bs4.element.PageElement | None:
        """Get next sibling tag."""

        sibling = el.next_sibling
        while tags and not isinstance(sibling, bs4.Tag) and sibling is not None:
            sibling = sibling.next_sibling

        if tags and not isinstance(sibling, bs4.Tag):
            sibling = None

        return sibling

    @classmethod
    def get_previous_tag(cls, el: bs4.Tag, tags: bool = True) -> bs4.Tag | None:
        """Get previous sibling tag."""

        return cls.get_previous(el, True)  # type: ignore[return-value]

    @classmethod
    def get_previous(cls, el: bs4.Tag, tags: bool = False) -> bs4.element.PageElement | None:
        """Get previous sibling tag."""

        sibling = el.previous_sibling
        while tags and not isinstance(sibling, bs4.Tag) and sibling is not None:
            sibling = sibling.previous_sibling

        if tags and not isinstance(sibling, bs4.Tag):
            sibling = None

        return sibling

    @staticmethod
    def has_html_ns(el: bs4.Tag | None) -> bool:
        """
        Check if element has an HTML namespace.

        This is a bit different than whether a element is treated as having an HTML namespace,
        like we do in the case of `is_html_tag`.
        """

        ns = getattr(el, 'namespace') if el is not None else None  # noqa: B009
        return bool(ns and ns == NS_XHTML)

    @staticmethod
    def split_namespace(el: bs4.Tag | None, attr_name: str) -> tuple[str | None, str | None]:
        """Return namespace and attribute name without the prefix."""

        if el is None:  # pragma: no cover
            return None, None

        return getattr(attr_name, 'namespace', None), getattr(attr_name, 'name', None)

    @classmethod
    def get_attribute_by_name(
        cls,
        el: bs4.Tag,
        name: str,
        default: str | Sequence[str] | None = None
    ) -> str | Sequence[str] | None:
        """Get attribute by name."""

        value = default
        if el._is_xml:
            if name in el.attrs:
                v = el.attrs[name]
                value = '' if v is None else v
        else:
            for k, v in el.attrs.items():
                if util.lower(k) == name:
                    value = '' if v is None else v
                    break
        return value

    @classmethod
    def iter_attributes(cls, el: bs4.Tag | None) -> Iterator[tuple[str, str | Sequence[str] | None]]:
        """Iterate attributes."""

        if el is not None:
            for k, v in el.attrs.items():
                yield k, '' if v is None else v

    @classmethod
    def get_classes(cls, el: bs4.Tag) -> Sequence[str]:
        """Get classes."""

        classes = cls.get_attribute_by_name(el, 'class', [])
        if isinstance(classes, str):
            classes = RE_NOT_WS.findall(classes)
        return cast(Sequence[str], classes)

    def get_text(self, el: bs4.Tag) -> str:
        """Get text."""

        return ''.join(
            [
                node for node in self.get_descendants(el)  # type: ignore[misc]
                if self.is_content_string(node)
            ]
        )

    def get_own_text(self, el: bs4.Tag) -> list[str]:
        """Get Own Text."""

        return [
            node for node in self.get_contents(el) if self.is_content_string(node)  # type: ignore[misc]
        ]


class Inputs:
    """Class for parsing and validating input items."""

    @staticmethod
    def validate_day(year: int, month: int, day: int) -> bool:
        """Validate day."""

        max_days = LONG_MONTH
        if month == FEB:
            max_days = FEB_LEAP_MONTH if ((year % 4 == 0) and (year % 100 != 0)) or (year % 400 == 0) else FEB_MONTH
        elif month in MONTHS_30:
            max_days = SHORT_MONTH
        return 1 <= day <= max_days

    @staticmethod
    def validate_week(year: int, week: int) -> bool:
        """Validate week."""

        # Validate an ISO week number for `year`.
        #
        # Per ISO 8601 rules, the last ISO week of a year is the week
        # containing Dec 28. Using Dec 28 guarantees we obtain the
        # correct ISO week-number for the final week of `year`, even in
        # years where Dec 31 falls in ISO week 01 of the following year.
        #
        # Example: if Dec 31 is a Thursday the year's last ISO week will
        # be week 53; if Dec 31 is a Monday and that week is counted as
        # week 1 of the next year, Dec 28 still belongs to the final
        # week of the current ISO year and yields the correct max week.
        #
        # `datetime` stops at year 9999, but the calendar repeats every 400
        # years (a whole number of weeks), so use the matching year from 2000
        # to 2399, as `calendar.weekday()` does.
        max_week = datetime(2000 + year % 400, 12, 28).isocalendar()[1]
        return 1 <= week <= max_week

    @staticmethod
    def validate_month(month: int) -> bool:
        """Validate month."""

        return 1 <= month <= 12

    @staticmethod
    def validate_year(year: int) -> bool:
        """Validate year."""

        return 1 <= year

    @staticmethod
    def parse_year(year: str) -> int:
        """
        Parse year.

        A year is four or more digits with no upper limit, but `int()` refuses digit strings longer than
        `sys.get_int_max_str_digits()`, so treat a year that long as invalid.
        """

        try:
            return int(year, 10)
        except ValueError:
            return 0

    @staticmethod
    def validate_hour(hour: int) -> bool:
        """Validate hour."""

        return 0 <= hour <= 23

    @staticmethod
    def validate_minutes(minutes: int) -> bool:
        """Validate minutes."""

        return 0 <= minutes <= 59

    @staticmethod
    def validate_seconds(seconds: float) -> bool:
        """Validate seconds."""

        return 0 <= seconds < 60

    @classmethod
    def parse_value(cls, itype: str, value: str | None) -> tuple[float, ...] | None:
        """
        Parse an input's `value`, `min` or `max` attribute, returning `None` if it is not valid.

        The same strict syntax is used for all three attributes, which is what browsers do. The HTML
        spec only requires it for the value. It converts `min` and `max` with more lenient algorithms,
        but every browser rejects what only those algorithms accept:

        - `number` and `range`: the spec's "rules for parsing floating-point number values" skip
          leading whitespace, accept a leading `+`, and ignore trailing characters (`20abc`, `20.`,
          `2e+`). Browsers reject all of these, so a `min` or `max` must be a valid floating-point
          number. A number that overflows a double (`1e9999`) is an error in the spec too.
        - `time` and `datetime-local`: the spec's parser accepts any number of fractional second
          digits, but browsers reject more than three, as in a valid time string.
        - `date`, `month`, `week` and `datetime-local`: the spec has no upper limit on the year,
          but browsers reject a value after `MAX_INPUT_VALUE`.

        Dates and times need the whole string to match, with no surrounding whitespace, a year of four
        or more digits greater than zero, an upper-case `W` in a week, and a `T` or a single space
        between the date and time of a `datetime-local`. Browsers and the spec agree on these.
        """

        parsed: tuple[float, ...] | None = None
        if value is None:
            return value
        if itype == "date":
            m = RE_DATE.match(value)
            if m:
                year = cls.parse_year(m.group('year'))
                month = int(m.group('month'), 10)
                day = int(m.group('day'), 10)
                if cls.validate_year(year) and cls.validate_month(month) and cls.validate_day(year, month, day):
                    parsed = (year, month, day)
        elif itype == "month":
            m = RE_MONTH.match(value)
            if m:
                year = cls.parse_year(m.group('year'))
                month = int(m.group('month'), 10)
                if cls.validate_year(year) and cls.validate_month(month):
                    parsed = (year, month)
        elif itype == "week":
            m = RE_WEEK.match(value)
            if m:
                year = cls.parse_year(m.group('year'))
                week = int(m.group('week'), 10)
                if cls.validate_year(year) and cls.validate_week(year, week):
                    parsed = (year, week)
        elif itype == "time":
            m = RE_TIME.match(value)
            if m:
                hour = int(m.group('hour'), 10)
                minutes = int(m.group('minutes'), 10)
                seconds = float(m.group('seconds') or 0)
                if cls.validate_hour(hour) and cls.validate_minutes(minutes) and cls.validate_seconds(seconds):
                    parsed = (hour, minutes, seconds)
        elif itype == "datetime-local":
            m = RE_DATETIME.match(value)
            if m:
                year = cls.parse_year(m.group('year'))
                month = int(m.group('month'), 10)
                day = int(m.group('day'), 10)
                hour = int(m.group('hour'), 10)
                minutes = int(m.group('minutes'), 10)
                seconds = float(m.group('seconds') or 0)
                if (
                    cls.validate_year(year) and cls.validate_month(month) and cls.validate_day(year, month, day) and
                    cls.validate_hour(hour) and cls.validate_minutes(minutes) and cls.validate_seconds(seconds)
                ):
                    parsed = (year, month, day, hour, minutes, seconds)
        elif itype in ("number", "range"):
            m = RE_NUM.match(value)
            if m:
                number = float(m.group('value'))
                if isfinite(number):
                    parsed = (number,)
        if parsed is not None and itype in MAX_INPUT_VALUE and parsed > MAX_INPUT_VALUE[itype]:
            parsed = None
        return parsed


class CSSMatch(_DocumentNav):
    """Perform CSS matching."""

    def __init__(
        self,
        selectors: ct.SelectorList,
        scope: bs4.Tag | None,
        namespaces: ct.Namespaces | None,
        flags: int
    ) -> None:
        """Initialize."""

        self.assert_valid_input(scope)
        self.tag = scope
        self.cached_meta_lang: list[tuple[str, str]] = []
        self.cached_forms: dict[int, TreeForms] = {}
        self.cached_options: dict[int, dict[int, bool]] = {}
        self.selectors = selectors
        self.namespaces: ct.Namespaces | dict[str, str] = {} if namespaces is None else namespaces
        self.flags = flags
        self.enable_cache = not bool(self.flags & util.NOCACHE)
        self.nth_cache: dict[Hashable, NthState] = {}
        self.sib_cache: dict[Hashable, int] = {}
        self.anc_cache: dict[tuple[int, int], bool] = {}
        self.desc_cache: dict[tuple[int, int], bool] = {}
        self.dir_cache: dict[int, int] = {}
        self.lang_cache: dict[int, str] = {}
        self.edit_cache: dict[int, bool] = {}
        self.tree_cache: dict[int, bs4.Tag] = {}
        self.select_cache: dict[int, bs4.Tag | None] = {}
        self.index_cache: dict[int, dict[int, int]] = {}

        # Find the root element for the whole tree
        doc = scope
        parent = self.get_parent(doc, no_template=False)
        while parent:
            doc = parent
            parent = self.get_parent(doc, no_template=False)
        root: bs4.Tag | None = None
        if not self.is_doc(doc):
            root = doc
        else:
            for child in self.get_tag_children(doc):
                root = child
                break

        self.root = root
        self.scope = scope if scope is not doc else root
        self.has_html_namespace = self.has_html_ns(root)

        # Parsers either record a namespace for every element or for none. The root
        # alone can't be expected to be HTML as a non-HTML element may have been detached.
        self.has_namespaces = bool(self.get_uri(root))

        # A document can be both XML and HTML (XHTML)
        self.is_xml = self.is_xml_tree(doc)
        self.is_html = not self.is_xml or self.has_html_namespace

    def reset(self) -> None:  # pragma: no cover
        """Reset."""

        self.nth_cache.clear()
        self.sib_cache.clear()
        self.anc_cache.clear()
        self.desc_cache.clear()
        self.dir_cache.clear()
        self.lang_cache.clear()
        self.edit_cache.clear()
        self.tree_cache.clear()
        self.select_cache.clear()
        self.index_cache.clear()

    def supports_namespaces(self) -> bool:
        """Check if namespaces are supported in the HTML type."""

        return self.is_xml or self.has_namespaces

    def get_tag_ns(self, el: bs4.Tag | None) -> str:
        """Get tag namespace."""

        namespace = ''
        if el is None:  # pragma: no cover
            return namespace

        if self.supports_namespaces():
            ns = self.get_uri(el)
            if ns:
                namespace = ns
        else:
            namespace = NS_XHTML
        return namespace

    def is_html_tag(self, el: bs4.Tag | None) -> bool:
        """Check if tag is in HTML namespace."""

        return self.get_tag_ns(el) == NS_XHTML

    def get_tag(self, el: bs4.Tag | None) -> str | None:
        """Get tag."""

        name = self.get_tag_name(el)
        return util.lower(name) if name is not None and not self.is_xml else name

    def get_prefix(self, el: bs4.Tag) -> str | None:
        """Get prefix."""

        prefix = self.get_prefix_name(el)
        return util.lower(prefix) if prefix is not None and not self.is_xml else prefix

    @staticmethod
    def find_text_dir(text: str) -> int | None:
        """Get directionality from the first character of the text with a strong direction."""

        for c in text:
            direction = bidi.DIRECTIONS[bisect_right(bidi.STARTS, ord(c)) - 1]
            if direction != 'N':
                return ct.SEL_DIR_LTR if direction == 'L' else ct.SEL_DIR_RTL
        return None

    def find_bidi(self, el: bs4.Tag) -> int | None:
        """Get directionality from element text."""

        stack = [self.get_children(el)]
        while stack:
            for node in stack[-1]:

                # Analyze child text nodes
                if self.is_tag(node):

                    # Avoid analyzing certain elements specified in the specification. This only applies
                    # to HTML elements: the text of others (SVG, MathML, etc.) is analyzed like any other.
                    if self.is_html_tag(node) and (
                        self.get_tag(node) in AUTO_DIR_IGNORED_ELEMENTS or
                        DIR_MAP.get(util.lower(self.get_attribute_by_name(node, 'dir', '')), None) is not None
                    ):
                        continue  # pragma: no cover

                    # Check directionality of this node's text, then resume with the remaining siblings.
                    stack.append(self.get_children(node))
                    break

                # Skip `doctype` comments, etc.
                if self.is_special_string(node):
                    continue

                # Analyze text nodes for directionality.
                direction = self.find_text_dir(cast('bs4.element.NavigableString', node))
                if direction is not None:
                    return direction
            else:
                # All children have been analyzed and direction could not be determined.
                stack.pop()
        return None

    def extended_language_filter(self, lang_range: str, lang_tag: str) -> bool:
        """
        Filter the language tags.

        `lang_tag` is a well-formed language tag, or an empty string if the language is untagged.
        """

        # Only an empty range matches an untagged language, and a range that is not well-formed matches nothing.
        if not lang_range or not lang_tag:
            return not lang_range and not lang_tag
        if RE_LANG_RANGE.fullmatch(lang_range) is None:
            return False

        match = True
        # Wildcards after the first subtag are ignored (RFC 4647 section 3.3.2, step 3.A), as subtags in between are
        # already skipped implicitly. So `en-*` is the same as `en` and `de-*-DE` the same as `de-DE`.
        first, *rest = lang_range.lower().split('-')
        ranges = [first, *(r for r in rest if r != '*')]
        subtags = lang_tag.lower().split('-')
        length = len(ranges)
        rindex = 0
        sindex = 0
        r = ranges[rindex]
        s = subtags[sindex]

        # Primary tag needs to match
        if r != '*' and r != s:
            match = False

        rindex += 1
        sindex += 1

        # Match until we run out of ranges
        while match and rindex < length:
            r = ranges[rindex]
            try:
                s = subtags[sindex]
            except IndexError:
                # Ran out of subtags,
                # but we still have ranges
                match = False
                continue

            # Matched range
            if s == r:
                rindex += 1

            # Implicit wildcard cannot match
            # singletons
            elif len(s) == 1:
                match = False
                continue

            # Implicitly matched, so grab next subtag
            sindex += 1

        return match

    def match_attribute_name(
        self,
        el: bs4.Tag,
        attr: str,
        prefix: str | None
    ) -> str | Sequence[str] | None:
        """Match attribute name and return value if it exists."""

        value = None
        if self.supports_namespaces():
            value = None
            # If we have not defined namespaces, we can't very well find them, so don't bother trying.
            if prefix:
                ns = self.namespaces.get(prefix)
                if ns is None and prefix != '*':
                    return None
            else:
                ns = None

            for k, v in self.iter_attributes(el):

                # Get attribute parts
                namespace, name = self.split_namespace(el, k)

                # Can't match a prefix attribute as we haven't specified one to match
                # Try to match it normally as a whole `p:a` as selector may be trying `p\:a`.
                if ns is None:
                    if (self.is_xml and attr == k) or (not self.is_xml and util.lower(attr) == util.lower(k)):
                        value = v
                        break
                    # Coverage is not finding this even though it is executed.
                    # Adding a print statement before this (and erasing coverage) causes coverage to find the line.
                    # Ignore the false positive message.
                    continue  # pragma: no cover

                # We can't match our desired prefix attribute as the attribute doesn't have a prefix
                if namespace is None or (ns != namespace and prefix != '*'):
                    continue

                # The attribute doesn't match.
                if (util.lower(attr) != util.lower(name)) if not self.is_xml else (attr != name):
                    continue

                value = v
                break
        else:
            for k, v in self.iter_attributes(el):
                if util.lower(attr) != util.lower(k):
                    continue
                value = v
                break
        return value

    def match_namespace(self, el: bs4.Tag, tag: ct.SelectorTag) -> bool:
        """Match the namespace of the element."""

        match = True
        namespace = self.get_tag_ns(el)
        default_namespace = self.namespaces.get('')
        tag_ns = '' if tag.prefix is None else self.namespaces.get(tag.prefix)
        # We must match the default namespace if one is not provided
        if tag.prefix is None and (default_namespace is not None and namespace != default_namespace):
            match = False
        # If we specified `|tag`, we must not have a namespace.
        elif (tag.prefix is not None and tag.prefix == '' and namespace):
            match = False
        # Verify prefix matches
        elif (
            tag.prefix and
            tag.prefix != '*' and (tag_ns is None or namespace != tag_ns)
        ):
            match = False
        return match

    def match_attributes(self, el: bs4.Tag, attributes: tuple[ct.SelectorAttribute, ...]) -> bool:
        """Match attributes."""

        match = True
        if attributes:
            for a in attributes:
                temp = self.match_attribute_name(el, a.attribute, a.prefix)
                # The case insensitive pattern only applies to HTML elements in HTML documents
                pattern: re.Pattern[str] | None
                if a.xml_type_pattern and (self.is_xml or not self.is_html_tag(el)):
                    pattern = a.xml_type_pattern
                    ignore_case = False
                else:
                    pattern = a.pattern
                    ignore_case = a.ignore_case
                if temp is None and a.inverse:
                    continue
                if temp is None:
                    match = False
                    break
                if pattern is None:
                    continue
                value = temp if isinstance(temp, str) else ' '.join(temp)
                # A case insensitive pattern is compiled for the value converted to ASCII lowercase.
                if ignore_case:
                    value = util.ascii_lower(value)
                if pattern.search(value) is None:
                    match = False
                    break
        return match

    def match_tagname(self, el: bs4.Tag, tag: ct.SelectorTag) -> bool:
        """Match tag name."""

        name = tag.name
        if name is None or name == '*':
            return True
        if self.is_xml:
            return name == el.name
        el_name = el.name
        return util.lower(name) == (util.lower(el_name) if el_name is not None else el_name)

    def match_tag(self, el: bs4.Tag, tag: ct.SelectorTag | None) -> bool:
        """Match the tag."""

        match = True
        if tag is not None:
            # Verify namespace
            if not self.match_tagname(el, tag):
                match = False
            if match and not self.match_namespace(el, tag):
                match = False
        return match

    def within(self, target: bs4.Tag, parent: bs4.Tag | bs4.BeautifulSoup, start: int, end: int | None = None) -> bool:
        """
        Check if target is within the parent's contents from `start` up to `end`.

        Searching the contents for every target is quadratic on parents with many children,
        so cache the index of each child the first time a parent is checked.
        """

        key = id(parent)
        if key not in self.index_cache:
            self.index_cache[key] = {id(child): i for i, child in enumerate(parent.contents)}
        index = self.index_cache[key].get(id(target), -1)
        return start <= index < (end if end is not None else len(parent.contents))

    def match_general_sibling(self, el: bs4.Tag, relation: ct.SelectorList) -> MatchGenerator:
        """Match general sibling combinator."""

        found = False

        if relation[0] is ct.Null:  # pragma: no cover
            return found

        pkey: int | None = None
        key: tuple[int, int] | None = None

        # Get the parent and use the ID for the cache key. Siblings do not need a parent element:
        # they can be top level elements in the document or the top level contents of a `template`.
        # Only an element that has been detached has no parent, and it has no siblings.
        parent = self.get_parent(el, no_doc=False, no_template=False)
        if parent is None:
            return found
        pkey = id(parent)

        # Check the cache to see if we already know where the first sibling is,
        # and if we do, check if we are on the correct side of it.
        # If we've previously searched and found no sibling, there is no sibling.
        # Lastly, if this is our first time, setup the cache.
        reverse = relation[0].rel_type == REL_HAS_SIBLING
        start = len(parent) - 1 if reverse else 0
        if pkey is not None:
            key = (pkey, id(relation))
            if key in self.sib_cache:
                index = self.sib_cache[key]
                if index >= 0:
                    a, b = (index + 1, start + 1) if reverse else (start, index)
                    return not self.within(el, parent, a, b)
                else:
                    return False
            self.sib_cache[key] = start

        # Start at the furthest endpoint and walk back towards the element looking for siblings.
        # The current element counts as a sibling, but will not cause a match.
        passed = False
        incr = -1 if reverse else 1
        for child in self.get_children(parent, start=start, reverse=reverse, template_contents=True):
            start += incr
            if not isinstance(child, bs4.Tag):
                continue

            # Flag that we are passing the element.
            # Any siblings we find aren't valid for this element.
            if child is el:
                passed = True

            # We found the furthest sibling.
            if (yield child, relation):
                found = True
                break

        # Cache the index of the sibling or mark as there being no siblings.
        if pkey and key:
            self.sib_cache[key] = start if found else -1

        # If we passed the current element and then found a sibling, it doesn't count as a match.
        if passed:
            found = False

        return found

    def match_ancestor(self, el: bs4.Tag, relation: ct.SelectorList) -> MatchGenerator:
        """
        Match descendant combinator.

        A selector with several descendant combinators tries every ancestor for each one, which is
        a polynomial of the depth with a degree of the number of combinators. Every element visited
        on the walk shares the result, so cache whether each one is or has a matching ancestor.
        """

        found = False
        visited: list[tuple[int, int]] = []
        parent: bs4.Tag | None = el
        while not found and parent and (parent := self.get_parent(parent)):
            key = (id(parent), id(relation))
            if key in self.anc_cache:
                found = self.anc_cache[key]
                break
            visited.append(key)
            found = yield parent, relation

        for key in visited:
            self.anc_cache[key] = found

        return found

    def match_past_relations(self, el: bs4.Tag, relation: ct.SelectorList) -> MatchGenerator:
        """Match past relationship."""

        found = False
        # I don't think this can ever happen, but it makes `mypy` happy
        if relation[0] is ct.Null:  # pragma: no cover
            return found

        if relation[0].rel_type == REL_PARENT:
            if self.enable_cache:
                found = yield from self.match_ancestor(el, relation)
            else:
                parent: bs4.Tag | None = el
                while not found and parent and (parent := self.get_parent(parent)):
                    found = parent is not None and (yield parent, relation)
        elif relation[0].rel_type == REL_CLOSE_PARENT:
            parent = self.get_parent(el)
            if parent is None:
                pass
            elif self.enable_cache:
                # Siblings share the parent, so cache whether it matches to avoid evaluating it again for every child.
                # This is inlined, instead of going through `match_ancestor`, as an extra generator is costly on
                # documents where most parents have a single child and the cache rarely gets a hit.
                key = (id(parent), id(relation))
                cached = self.anc_cache.get(key)
                if cached is None:
                    cached = self.anc_cache[key] = yield parent, relation
                found = cached
            else:
                found = yield parent, relation
        elif relation[0].rel_type == REL_SIBLING:
            if self.enable_cache:
                found = yield from self.match_general_sibling(el, relation)
            else:
                sibling: bs4.Tag | None = el
                while not found and sibling and (sibling := self.get_previous_tag(sibling)):
                    found = sibling is not None and (yield sibling, relation)
        elif relation[0].rel_type == REL_CLOSE_SIBLING:
            sibling = self.get_previous_tag(el)
            found = sibling is not None and (yield sibling, relation)
        return found

    def match_future_child(
        self,
        parent: bs4.Tag,
        relation: ct.SelectorList,
        recursive: bool = False
    ) -> MatchGenerator:
        """Match future child."""

        match = False
        if recursive:
            children: Callable[..., Iterator[bs4.Tag]] = self.get_tag_descendants
        else:
            children = self.get_tag_children
        for child in children(parent):
            if (yield child, relation):
                match = True
                break
        return match

    def match_descendant(self, el: bs4.Tag, relation: ct.SelectorList) -> MatchGenerator:
        """
        Match descendant relation of `:has()`.

        Every element matched by `:has()` searches all of its descendants, which is quadratic in the depth
        of the document. Every element visited on the search shares the result, so cache whether each one
        is or has a matching descendant: an element searched without a match is cached along with all of
        its descendants, and when there is a match, the element and all of those it is nested in are cached.
        """

        found = False
        # Elements whose contents are being searched, outermost first, and elements searched without a match
        searching: list[tuple[int, int]] = []
        searched: list[tuple[int, int]] = []
        stack = [self.get_tag_children(el)]
        while stack and not found:
            for child in stack[-1]:
                key = (id(child), id(relation))
                if key in self.desc_cache:
                    if self.desc_cache[key]:
                        found = True
                        break
                    continue
                searching.append(key)
                if (yield child, relation):
                    found = True
                else:
                    stack.append(self.get_tag_children(child))
                break
            else:
                stack.pop()
                if searching:
                    searched.append(searching.pop())

        for key in searching:
            self.desc_cache[key] = True
        for key in searched:
            self.desc_cache[key] = False

        return found

    def match_future_relations(self, el: bs4.Tag, relation: ct.SelectorList) -> MatchGenerator:
        """Match future relationship."""

        found = False
        # I don't think this can ever happen, but it makes `mypy` happy
        if relation[0] is ct.Null:  # pragma: no cover
            return found

        if relation[0].rel_type == REL_HAS_PARENT:
            if self.enable_cache:
                found = yield from self.match_descendant(el, relation)
            else:
                found = yield from self.match_future_child(el, relation, True)
        elif relation[0].rel_type == REL_HAS_CLOSE_PARENT:
            found = yield from self.match_future_child(el, relation)
        elif relation[0].rel_type == REL_HAS_SIBLING:
            if self.enable_cache:
                found = yield from self.match_general_sibling(el, relation)
            else:
                sibling: bs4.Tag | None = el
                while not found and sibling and (sibling := self.get_next_tag(sibling)):
                    found = yield sibling, relation
        elif relation[0].rel_type == REL_HAS_CLOSE_SIBLING:
            sibling = self.get_next_tag(el)
            found = sibling is not None and (yield sibling, relation)
        return found

    def match_relations(self, el: bs4.Tag, relation: ct.SelectorList) -> MatchGenerator:
        """Match relationship to other elements."""

        sel = relation[0]
        if sel is not ct.Null and sel.rel_type is not None and sel.rel_type.startswith(':'):
            return self.match_future_relations(el, relation)
        return self.match_past_relations(el, relation)

    def match_id(self, el: bs4.Tag, ids: tuple[str, ...]) -> bool:
        """Match element's ID."""

        found = True
        for i in ids:
            if i != self.get_attribute_by_name(el, 'id', ''):
                found = False
                break
        return found

    def match_classes(self, el: bs4.Tag, classes: tuple[str, ...]) -> bool:
        """Match element's classes."""

        current_classes = self.get_classes(el)
        found = True
        for c in classes:
            if c not in current_classes:
                found = False
                break
        return found

    def match_root(self, el: bs4.Tag) -> bool:
        """Match element as root."""

        is_root = self.is_root(el)
        if is_root:
            sibling: Any = self.get_previous(el)
            while is_root and sibling is not None:
                if (
                    self.is_tag(sibling) or (self.is_content_string(sibling) and sibling.strip()) or
                    self.is_cdata(sibling)
                ):
                    is_root = False
                else:
                    sibling = self.get_previous(sibling)
        if is_root:
            sibling = self.get_next(el)
            while is_root and sibling is not None:
                if (
                    self.is_tag(sibling) or (self.is_content_string(sibling) and sibling.strip()) or
                    self.is_cdata(sibling)
                ):
                    is_root = False
                else:
                    sibling = self.get_next(sibling)
        return is_root

    def match_scope(self, el: bs4.Tag) -> bool:
        """Match element as scope."""

        return self.scope is el

    def match_nth_tag_type(self, el: bs4.Tag, child: bs4.Tag) -> bool:
        """Match tag type for `nth` matches."""

        return (
            (self.get_tag(child) == self.get_tag(el)) and
            (self.get_tag_ns(child) == self.get_tag_ns(el))
        )

    def match_nth_candidate(self, el: bs4.Tag, child: bs4.Tag, n: ct.SelectorNth) -> MatchGenerator:
        """Check if a sibling is counted by the `nth` selector (`of-type` or `of S`)."""

        if n.of_type and not self.match_nth_tag_type(el, child):
            return False
        if n.selectors and not (yield child, n.selectors):
            return False
        return True

    def get_nth_index(
        self,
        el: bs4.Tag,
        parent: bs4.Tag | bs4.BeautifulSoup | None,
        n: ct.SelectorNth
    ) -> Generator[MatchRequest, bool, int]:
        """
        Get the 1-based index of `el` among the siblings counted by `n`, or 0 if `el` is not counted.

        When caching is enabled, each sibling is evaluated against `n` at most once per parent.
        The siblings are scanned lazily in document order and the position of every counted sibling
        is recorded, so a lookup costs amortized O(1) no matter what order elements are queried in.
        This matters as an `nth` selector's `of S` may contain other `nth` selectors, which will then
        be queried in whatever order the outer selector walks its siblings.
        """

        # No parent, just evaluate the element against the selectors
        if parent is None:
            return 1 if (yield from self.match_nth_candidate(el, el, n)) else 0

        # Non-cached handling of parented element
        if not self.enable_cache:
            index = 0
            for child in self.get_children(parent, reverse=n.last, tags=True, template_contents=True):
                if (yield from self.match_nth_candidate(el, child, n)):
                    index += 1
                    if child is el:
                        return index
                elif child is el:
                    break
            return 0

        key = (
            (id(parent), id(n), self.get_tag(el), self.get_tag_ns(el))
            if n.of_type
            else (id(parent), id(n), None, None)
        )
        state = self.nth_cache.get(key)
        if state is None:
            # Next content position to scan, number of counted siblings found, and their positions.
            state = self.nth_cache[key] = NthState(0, 0, {})

        # "Last" selectors need the total count, so scan all siblings, otherwise just scan up to `el`.
        contents = parent.contents
        if n.last:
            end = len(contents)
        else:
            pkey = id(parent)
            if pkey not in self.index_cache:
                self.index_cache[pkey] = {id(child): i for i, child in enumerate(contents)}
            end = self.index_cache[pkey][id(el)] + 1

        while state.pos < end:
            node = contents[state.pos]
            if self.is_tag(node) and (yield from self.match_nth_candidate(el, cast('bs4.Tag', node), n)):
                state.count += 1
                state.positions[id(node)] = state.count
            state.pos += 1

        index = state.positions.get(id(el), 0)
        if index and n.last:
            index = state.count - index + 1
        return index

    def match_nth(self, el: bs4.Tag, nth: tuple[ct.SelectorNth, ...]) -> MatchGenerator:
        """Match `nth` elements."""

        # The top level contents of a `template` have no parent element, but they do have siblings.
        parent = self.get_parent(el, no_doc=False, no_template=False)

        for n in nth:
            index = yield from self.get_nth_index(el, parent, n)
            if not index:
                return False
            if n.a != 0:
                v = (index - n.b) / n.a
                if not (v.is_integer() and v >= 0):
                    return False
            elif index != n.b:
                return False
        return True

    def match_empty(self, el: bs4.Tag) -> bool:
        """Check if element is empty (if requested)."""

        is_empty = True
        child: Any
        for child in self.get_children(el):
            if self.is_tag(child):
                is_empty = False
                break
            elif self.is_content_string(child) and RE_NOT_EMPTY.search(child):
                is_empty = False
                break
        return is_empty

    def match_subselectors(self, el: bs4.Tag, selectors: tuple[ct.SelectorList, ...]) -> MatchGenerator:
        """Match selectors."""

        match = True
        for sel in selectors:
            if not (yield el, sel):
                match = False
        return match

    def match_contains(self, el: bs4.Tag, contains: tuple[ct.SelectorContains, ...]) -> bool:
        """Match element if it contains text."""

        match = True
        content: str | None = None
        own_content: Sequence[str] | None = None
        for contain_list in contains:
            found = False
            for text in contain_list.text:
                if contain_list.own:
                    if own_content is None:
                        own_content = self.get_own_text(el)
                    for c in own_content:
                        if text in c:
                            found = True
                            break
                    if found:
                        break
                else:
                    if content is None:
                        content = self.get_text(el)
                    if text in content:
                        found = True
                        break
            if not found:
                match = False
        return match

    def get_tree(self, el: bs4.Tag) -> bs4.Tag:
        """Get the top of the tree that an element is in: its document or `template`."""

        # Every element visited shares the top of the tree, so cache it to avoid walking the same ancestors again.
        visited: list[int] = []
        top = el
        parent = self.get_parent(top, no_doc=False, no_template=False)
        while parent is not None:
            key = id(top)
            if key in self.tree_cache:
                top = self.tree_cache[key]
                break
            visited.append(key)
            top = parent
            if self.is_template(top):
                break
            parent = self.get_parent(top, no_doc=False, no_template=False)
        if self.enable_cache:
            for key in visited:
                self.tree_cache[key] = top
        return top

    def is_submit_button(self, el: bs4.Tag) -> bool:
        """Check if an `input` or `button` is a submit button."""

        v = util.lower(cast(str, self.get_attribute_by_name(el, 'type', '')))
        if self.get_tag(el) == 'input':
            return v in ('submit', 'image')

        # A button with a missing or invalid `type` submits, unless it invokes a command or is
        # a child of a `select`.
        return v == 'submit' or (
            v not in ('button', 'reset') and
            self.get_attribute_by_name(el, 'command') is None and
            self.get_attribute_by_name(el, 'commandfor') is None and
            self.get_tag(self.get_parent(el)) != 'select'
        )

    def get_forms(self, tree: bs4.Tag) -> TreeForms:
        """
        Get the form information of a tree.

        A form owner is the form that the `form` attribute refers to, if there is one, otherwise the nearest
        ancestor form. The attribute is only used by elements connected to a document, which the contents of
        a `template` are not. A `template` holds its own tree, so it is not entered.

        This requires a pass through the whole tree as the `form` attribute can refer to any form, so it is
        cached instead of searching the tree again for every element.

        A form in a form only exists in XML and in a `template`. Anywhere else, it can only have come from a
        parser that doesn't follow the HTML specification. A browser's parser would have ignored the nested
        form and taken its end tag for that of the parent, so what it holds belongs to the parent, and
        what follows it does not.
        """

        key = id(tree)
        forms = self.cached_forms.get(key)
        if forms is not None:
            return forms

        ids: dict[str, bs4.Tag] = {}
        controls: list[tuple[bs4.Tag, bs4.Tag | None]] = []
        closed: set[int] = set()
        nesting = self.is_xml or self.is_template(tree)
        stack: list[tuple[bs4.Tag | None, bs4.Tag | None]] = [
            (el, None) for el in self.get_children(tree, reverse=True, tags=True, template_contents=True)
        ]
        while stack:
            el, form = stack.pop()

            # The end of a nested form, which ends the form it is in
            if el is None:
                closed.add(id(form))
                continue

            if id(form) in closed:
                form = None
            name = self.get_tag(el) if self.is_html_tag(el) else None
            if name == 'form' and form is not None and not nesting:
                stack.append((None, form))
            else:
                el_id = cast(str, self.get_attribute_by_name(el, 'id', ''))
                if el_id:
                    ids.setdefault(el_id, el)
                if name == 'form':
                    form = el
                elif name in ('input', 'button'):
                    controls.append((el, form))
            stack.extend((child, form) for child in self.get_tag_children(el, reverse=True))

        forms = TreeForms({}, {}, {})
        connected = not self.is_template(tree)
        for el, form in controls:
            form_id = self.get_attribute_by_name(el, 'form')
            if connected and form_id is not None:
                form = ids.get(cast(str, form_id))
                if form is not None and not (self.get_tag(form) == 'form' and self.is_html_tag(form)):
                    form = None
            forms.owners[id(el)] = form

            # Controls are in tree order, so the first submit button of a form is its default button,
            # and the last radio button with `checked` in a radio button group is the one left checked.
            radio_name = cast(str, self.get_attribute_by_name(el, 'name', ''))
            if form is not None and self.is_submit_button(el):
                forms.defaults.setdefault(id(form), el)
            elif (
                radio_name and
                self.get_attribute_by_name(el, 'checked') is not None and
                util.lower(cast(str, self.get_attribute_by_name(el, 'type', ''))) == 'radio'
            ):
                forms.checked[(id(form), radio_name)] = el

        self.cached_forms[key] = forms
        return forms

    def match_default(self, el: bs4.Tag) -> bool:
        """Match the default button of a form, which is the first submit button in the tree that it owns."""

        forms = self.get_forms(self.get_tree(el))
        form = forms.owners.get(id(el))
        return form is not None and forms.defaults.get(id(form)) is el

    def get_radio_group(self, el: bs4.Tag, forms: TreeForms) -> tuple[int, str]:
        """Get the key of the radio button group of a radio button with a name."""

        return id(forms.owners.get(id(el))), cast(str, self.get_attribute_by_name(el, 'name'))

    def match_indeterminate(self, el: bs4.Tag) -> bool:
        """Match a radio button in a radio button group that has no checked radio buttons."""

        forms = self.get_forms(self.get_tree(el))
        return self.get_radio_group(el, forms) not in forms.checked

    def match_checked_radio(self, el: bs4.Tag) -> bool:
        """
        Match a radio button with a `checked` attribute if it is still checked.

        A radio button that is checked unchecks the others in its group when it is added to a document, so
        only the last one is left checked. The contents of a `template` are never added to a document,
        so all of them stay checked.
        """

        if not self.get_attribute_by_name(el, 'name'):
            return True

        tree = self.get_tree(el)
        if self.is_template(tree):
            return True

        forms = self.get_forms(tree)
        return forms.checked.get(self.get_radio_group(el, forms)) is el

    def get_options(self, select: bs4.Tag) -> dict[int, bool]:
        """
        Get the selectedness of each option in the list of options of a `select`.

        An option that is not in a `select` with `multiple` is left selected if it is the last one with
        a `selected` attribute, and if there are none, the first enabled option is selected when
        the `select` shows a single option.

        Other than in XML, a nested `option`, `optgroup`, or `select` can only have come from a parser that
        doesn't follow the HTML specification. A browser's parser would have ended the first one, so
        an `option` or `optgroup` is treated as a sibling, and a `select` ends the list of options.
        """

        key = id(select)
        selectedness = self.cached_options.get(key)
        if selectedness is not None:
            return selectedness

        options: list[tuple[bs4.Tag, bool]] = []
        stack: list[tuple[bs4.Tag, bool | None]] = [
            (el, None) for el in self.get_tag_children(select, reverse=True)
        ]
        while stack:
            el, group = stack.pop()
            name = self.get_tag(el) if self.is_html_tag(el) else None
            if name == 'select' and not self.is_xml:
                break
            if name == 'option':
                options.append((el, bool(group)))
            if name in ('select', 'hr', 'datalist') or (
                self.is_xml and (name == 'option' or (name == 'optgroup' and group is not None))
            ):
                continue
            if name == 'optgroup':
                group = self.get_attribute_by_name(el, 'disabled') is not None
            stack.extend((child, group) for child in self.get_tag_children(el, reverse=True))

        marked = [el for el, _ in options if self.get_attribute_by_name(el, 'selected') is not None]
        if self.get_attribute_by_name(select, 'multiple') is None:
            size = RE_SIZE.match(cast(str, self.get_attribute_by_name(select, 'size', '')))
            if marked:
                marked = marked[-1:]
            elif size is None or size.group('value').lstrip('0') in ('', '1'):
                marked = [
                    el for el, disabled in options
                    if not disabled and self.get_attribute_by_name(el, 'disabled') is None
                ][:1]

        selected = {id(el) for el in marked}
        selectedness = {id(el): id(el) in selected for el, _ in options}
        self.cached_options[key] = selectedness
        return selectedness

    def match_checked_option(self, el: bs4.Tag) -> bool:
        """Match a selected option. Without a `select`, an option is only selected by its `selected` attribute."""

        # Every element visited shares the `select`, so cache it to avoid walking the same ancestors again.
        visited: list[int] = []
        node = el
        while True:
            key = id(node)
            if key in self.select_cache:
                parent = self.select_cache[key]
                break
            visited.append(key)
            parent = self.get_parent(node)
            if parent is None or (self.get_tag(parent) == 'select' and self.is_html_tag(parent)):
                break
            node = parent
        if self.enable_cache:
            for key in visited:
                self.select_cache[key] = parent

        selected = self.get_options(parent).get(id(el)) if parent is not None else None
        return self.get_attribute_by_name(el, 'selected') is not None if selected is None else selected

    def match_lang(self, el: bs4.Tag, langs: tuple[ct.SelectorLang, ...]) -> bool:
        """Match languages."""

        # Every element visited while resolving shares the language,
        # so cache it to avoid walking the same ancestors again for each element.
        visited: list[int] = []
        found_lang = self.resolve_lang(el, visited)
        if self.enable_cache:
            for key in visited:
                self.lang_cache[key] = found_lang

        # A language tag that is not well-formed matches nothing, not even `:lang('')` or `:lang('*')`.
        if found_lang and RE_LANG_TAG.fullmatch(found_lang) is None:
            return False

        match = False
        for patterns in langs:
            match = False
            for pattern in patterns:
                if self.extended_language_filter(pattern, found_lang):
                    match = True
            if not match:
                break

        return match

    def resolve_lang(self, el: bs4.Tag, visited: list[int]) -> str:
        """Resolve the language of an element, which is an empty string if it is unknown."""

        has_ns = self.supports_namespaces()
        root = self.root
        has_html_namespace = self.has_html_namespace

        # Walk parents looking for `lang` (HTML) or `xml:lang` XML property.
        parent: bs4.Tag | None = el
        found_lang = None
        last = None
        while found_lang is None:
            key = id(parent)
            if key in self.lang_cache:
                return self.lang_cache[key]
            visited.append(key)
            has_html_ns = self.has_html_ns(parent)
            # SVG elements also use a `lang` attribute in no namespace, but `xml:lang` takes precedence.
            has_svg_ns = has_ns and getattr(parent, 'namespace', None) == NS_SVG
            svg_lang = None
            for k, v in self.iter_attributes(parent):
                attr_ns, attr = self.split_namespace(parent, k)
                if (
                    ((not has_ns or has_html_ns) and (util.lower(k) if not self.is_xml else k) == 'lang') or
                    (
                        has_ns and not has_html_ns and attr_ns == NS_XML and
                        (util.lower(attr) if not self.is_xml and attr is not None else attr) == 'lang'
                    )
                ):
                    found_lang = v
                    break
                if has_svg_ns and attr_ns is None and (util.lower(k) if not self.is_xml else k) == 'lang':
                    svg_lang = v
            if found_lang is None:
                found_lang = svg_lang
            last = parent
            parent = self.get_parent(parent)

            if parent is None:
                root = last
                has_html_namespace = self.has_html_ns(root)
                parent = last
                break

        # Use cached meta language.
        if found_lang is None and self.cached_meta_lang:
            for cache in self.cached_meta_lang:
                if root is not None and cast(str, root) is cache[0]:
                    return cache[1]

        # If we couldn't find a language, and the document is HTML, look to meta to determine language.
        if found_lang is None and (not self.is_xml or (has_html_namespace and root and root.name == 'html')):
            # Find head
            found = False
            for tag in ('html', 'head'):
                found = False
                for child in self.get_tag_children(parent):
                    if self.get_tag(child) == tag and self.is_html_tag(child):
                        found = True
                        parent = child
                        break
                if found:  # pragma: no cover
                    break

            # Search meta tags
            if found and parent is not None:
                for child2 in parent:
                    if isinstance(child2, bs4.Tag) and self.get_tag(child2) == 'meta' and self.is_html_tag(parent):
                        c_lang = False
                        content = None
                        for k, v in self.iter_attributes(child2):
                            if util.lower(k) == 'http-equiv' and util.lower(v) == 'content-language':
                                c_lang = True
                            if util.lower(k) == 'content':
                                content = v
                            if c_lang and content:
                                found_lang = content
                                self.cached_meta_lang.append((cast(str, root), cast(str, found_lang)))
                                break
                    if found_lang is not None:
                        break
                if found_lang is None:
                    self.cached_meta_lang.append((cast(str, root), ''))

        # Without any language information, the language is unknown, which is an empty language tag.
        return '' if found_lang is None else cast(str, found_lang)

    def match_dir(self, el: bs4.Tag, directionality: int) -> bool:
        """Check directionality."""

        # Direction is only supported in HTML documents.
        if not self.is_html:
            return False

        # If we have to match both left and right, we can't match either.
        if directionality & ct.SEL_DIR_LTR and directionality & ct.SEL_DIR_RTL:
            return False

        # Every element visited while resolving shares the resolved direction,
        # so cache it to avoid walking the same ancestors again for each element.
        visited: list[int] = []
        direction = self.resolve_dir(el, visited)
        if self.enable_cache:
            for key in visited:
                self.dir_cache[key] = direction
        return direction == directionality

    def resolve_dir(self, el: bs4.Tag, visited: list[int]) -> int:
        """Resolve the direction of an element."""

        while True:
            key = id(el)
            if key in self.dir_cache:
                return self.dir_cache[key]
            visited.append(key)

            # `dir` is only defined for HTML elements, so other elements (SVG, MathML, etc.)
            # always inherit the direction of their parent.
            if self.is_html_tag(el):

                # Element has defined direction of left to right or right to left
                direction = DIR_MAP.get(util.lower(self.get_attribute_by_name(el, 'dir', '')), None)
                if direction not in (None, 0):
                    return direction

                # If `input[type=telephone]` and no direction is assigned, assume left to right.
                name = self.get_tag(el)
                is_input = name == 'input'
                is_textarea = name == 'textarea'
                is_bdi = name == 'bdi'
                itype = util.lower(self.get_attribute_by_name(el, 'type', '')) if is_input else ''
                if is_input and itype == 'tel' and direction is None:
                    return ct.SEL_DIR_LTR

                # Auto handling for inputs whose value is used
                if ((is_input and itype not in NON_AUTO_DIR_INPUT_TYPES) or is_textarea) and direction == 0:
                    if is_textarea:
                        value = ''.join(
                            node for node in self.get_contents(el)  # type: ignore[misc]
                            if self.is_content_string(node)
                        )
                    else:
                        value = cast(str, self.get_attribute_by_name(el, 'value', ''))
                    direction = self.find_text_dir(value)
                    # No strong directional characters (or no value), assume left to right
                    return direction if direction is not None else ct.SEL_DIR_LTR

                # Auto handling for `bdi` and other non text inputs.
                if (is_bdi and direction is None) or direction == 0:
                    direction = self.find_bidi(el)
                    if direction is not None:
                        return direction
                    # Auto directionality could not be determined, assume left to right
                    return ct.SEL_DIR_LTR

            # Match parents direction. There may be no parent element, and not just for the root:
            # a fragment can have many top level elements, and a `template` has its own contents.
            # Without one, assume left to right.
            parent = self.get_parent(el)
            if parent is None:
                return ct.SEL_DIR_LTR
            el = parent

    def match_range(self, el: bs4.Tag, condition: int) -> bool:
        """
        Match `:in-range` or `:out-of-range`.

        Policy: follow the HTML spec, except where all browsers agree on something else, in which case
        follow the browsers and document the difference here or in `Inputs.parse_value()`. Before
        changing this behavior, check it against the spec and against Chrome, Firefox and Safari. A
        report that cites only the spec, or only one browser, is not enough. Where browsers disagree
        with each other, follow the spec. Known cases, all deliberate:

        - A `range` input whose maximum is less than its minimum: Chrome and Safari match `:in-range`,
          Firefox and the spec `:out-of-range` (see below).
        - Firefox and Safari do not support `month` or `week` inputs and treat them as text, so Chrome
          is the only browser to compare them with.
        - Safari accepts a `number` bound with a trailing `.` (`min="20."`), and Firefox accepts such
          a value. Neither is a valid floating-point number, and the other browsers reject them.

        https://html.spec.whatwg.org/multipage/semantics-other.html#selector-in-range

        - `:out-of-range` matches an input that is a candidate for constraint validation, has range
          limitations, and is suffering from an underflow or an overflow. `:in-range` matches one that
          is a candidate and has range limitations but suffers from neither.
        - Being a candidate for constraint validation is checked by `CSS_IN_RANGE` and
          `CSS_OUT_OF_RANGE`, which exclude inputs that are disabled, `readonly` or in a `datalist`.
        - An input has range limitations if it has a minimum or a maximum. Only `range` has defaults:
          0 and 100. For the other types, a `min` or `max` that does not parse is ignored, and an input
          with neither matches neither pseudo-class.
        - An input suffers from an underflow when its value is below its minimum, and from an overflow
          when it is above its maximum. An empty or invalid value is sanitized to the empty string,
          which suffers from neither, so it is in range.
        - `time` has a periodic domain. When its maximum is less than its minimum, it has a reversed
          range, such as 22:00 to 02:00 across midnight, and is out of range only when its value is
          both below the minimum and above the maximum.
        - The other types do not have a periodic domain. When the maximum is less than the minimum,
          any value is out of range, but an empty value is still in range.
        - A `range` input's value is clamped to its minimum and maximum, and an invalid or missing
          value becomes a default within them, so it is in range unless its maximum is less than its
          minimum. In that case, the value is raised to the minimum but is not lowered to the maximum,
          so it is always out of range. Chrome and Safari match it as in range instead, which is a
          browser bug; Firefox follows the spec, and so do we.
        """

        out_of_range = False

        itype = util.lower(cast(str, self.get_attribute_by_name(el, 'type', '')))

        # A `range` input always has a minimum and maximum, 0 and 100 by default, and its value
        # is clamped to them, so it is out of range only when its maximum is less than its minimum.
        if itype == 'range':
            mn = Inputs.parse_value(itype, cast(str, self.get_attribute_by_name(el, 'min', None)))
            mx = Inputs.parse_value(itype, cast(str, self.get_attribute_by_name(el, 'max', None)))
            out_of_range = (mx or (100.0,)) < (mn or (0.0,))
            return not out_of_range if condition & ct.SEL_IN_RANGE else out_of_range

        mn = Inputs.parse_value(itype, cast(str, self.get_attribute_by_name(el, 'min', None)))
        mx = Inputs.parse_value(itype, cast(str, self.get_attribute_by_name(el, 'max', None)))

        # There is no valid min or max, so we cannot evaluate a range
        if mn is None and mx is None:
            return False

        value = Inputs.parse_value(itype, cast(str, self.get_attribute_by_name(el, 'value', None)))
        if value is not None:
            if itype in ("date", "datetime-local", "month", "week", "number"):
                if mn is not None and value < mn:
                    out_of_range = True
                if not out_of_range and mx is not None and value > mx:
                    out_of_range = True
            elif itype == "time":
                if mn is not None and mx is not None and mn > mx:
                    # Time is periodic, so this is a reversed/discontinuous range
                    if value < mn and value > mx:
                        out_of_range = True
                else:
                    if mn is not None and value < mn:
                        out_of_range = True
                    if not out_of_range and mx is not None and value > mx:
                        out_of_range = True

        return not out_of_range if condition & ct.SEL_IN_RANGE else out_of_range

    def match_defined(self, el: bs4.Tag) -> bool:
        """
        Match defined.

        `:defined` is related to custom elements in a browser.

        - If the document is XML (not XHTML), no tags will match.
        - Tags that are not custom (don't have a hyphen) are marked defined.
        - If the tag has a prefix (without or without a namespace), it will not match.
        - HTML elements with an `is` attribute (customized built-in elements) are created as undefined,
          like custom tags, so they don't match.

        This is of course requires the parser to provide us with the proper prefix and namespace info,
        if it doesn't, there is nothing we can do.
        """

        name = self.get_tag(el)
        return (
            self.is_html and name is not None and (
                (name.find('-') == -1 and not self.is_customized_builtin(el)) or
                name.find(':') != -1 or
                self.get_prefix(el) is not None
            )
        )

    def is_customized_builtin(self, el: bs4.Tag) -> bool:
        """Check if an HTML element has an `is` attribute, which makes it a customized built-in element."""

        return self.is_html_tag(el) and self.get_attribute_by_name(el, 'is') is not None

    def match_placeholder_shown(self, el: bs4.Tag) -> bool:
        """
        Match placeholder shown according to HTML spec.

        - text area should be checked if they have content. A single newline does not count as content.
        - An input's value is sanitized first, and the placeholder is shown when the result is
          empty. Text-like inputs drop line breaks, `url` and `email` inputs also drop leading and
          trailing whitespace, and a `number` input empties a value that is not a valid
          floating-point number, so `value="abc"` still shows the placeholder.

        https://html.spec.whatwg.org/multipage/input.html#value-sanitization-algorithm

        As in `match_range()`, we follow the spec, except where all browsers agree on something
        else. Known cases, all deliberate:

        - A `number` value that overflows a double, such as `1e9999`, is emptied, as in Chrome,
          Firefox and Safari, although the spec's sanitization keeps it as a valid floating-point
          number.
        - Firefox keeps `value="1."` for a `number` input, so it hides the placeholder. It is not a
          valid floating-point number: Chrome, Safari and the spec empty it, and so do we.
        """

        name = self.get_tag(el)
        if name == 'input':
            itype = util.lower(cast(str, self.get_attribute_by_name(el, 'type', '')))
            value = cast(str, self.get_attribute_by_name(el, 'value', ''))
            value = value.replace('\r', '').replace('\n', '')
            if itype in ('url', 'email'):
                value = value.strip(' \t\f')
            elif itype == 'number' and Inputs.parse_value(itype, value) is None:
                value = ''
            return value == ''

        # This is checked for every element before the rest of the pattern, so only get the text of a `textarea`.
        return name == 'textarea' and self.get_text(el) in ('', '\n')

    def match_editable(self, el: bs4.Tag) -> bool:
        """
        Match editing hosts and the elements that are editable inside them.

        The nearest `contenteditable` attribute with a valid state decides: an empty value, `true` and
        `plaintext-only` make the element editable, and `false` does not. Any other value inherits
        the state of the parent. The attribute only applies to HTML elements, others (SVG, MathML,
        etc.) always inherit the state of their parent.
        """

        # Every element visited shares the state, so cache it to avoid walking the same ancestors again.
        visited: list[int] = []
        editable = False
        node = el  # type: bs4.Tag | None
        while node is not None:
            key = id(node)
            if key in self.edit_cache:
                editable = self.edit_cache[key]
                break
            visited.append(key)
            state = self.get_attribute_by_name(node, 'contenteditable') if self.is_html_tag(node) else None
            if isinstance(state, str):
                state = util.lower(state)
                if state in ('', 'true', 'plaintext-only'):
                    editable = True
                    break
                if state == 'false':
                    break
            node = self.get_parent(node)
        if self.enable_cache:
            for key in visited:
                self.edit_cache[key] = editable
        return editable

    def match_selectors(self, el: bs4.Tag, selectors: ct.SelectorList) -> bool:
        """
        Check if element matches one of the selectors.

        Since some selectors can have complex relationships, they may need to recursively
        check if another relative element matches specific selectors. To avoid recursion,
        generators are yielded instead whenever it needs to check if a relative element
        satisfies specific selectors. These can be compared and the result sent back.
        We can check the end result of the generator once it is satisfied.
        """

        # Most matches never need a nested match, so avoid setting up the stack unless needed.
        # `frame` yields a `bool` when it is done.
        frame = self._match_selectors(el, selectors)
        item = next(frame)
        if item.__class__ is bool:
            return item

        # We need to setup a stack as a selector has sent back a match request.
        stack = [frame, self._match_selectors(*cast('MatchRequest', item))]
        result: bool | None = None
        while True:
            item = stack[-1].send(result)  # type: ignore[arg-type]
            if item.__class__ is bool:
                stack.pop()
                if not stack:
                    return item
                result = item
            else:
                stack.append(self._match_selectors(*cast('MatchRequest', item)))
                result = None

    def _match_selectors(self, el: bs4.Tag, selectors: ct.SelectorList) -> Generator[MatchRequest | bool, bool, None]:
        """
        Check if element matches one of the selectors, yielding requests to match nested selectors.

        Yields `MatchRequests` or affirmation as to whether a selector was matched. The match request
        requires a boolean to be sent back to communicate whether the match was successful.
        """

        match = False
        is_not = selectors.is_not
        is_html = selectors.is_html

        # Internal selector lists that use the HTML flag, will automatically get the `html` namespace.
        if is_html:
            namespaces = self.namespaces
            self.namespaces = {'html': NS_XHTML}

        if not is_html or self.is_html:
            for selector in selectors:
                match = is_not
                # We have a un-matchable situation (like `:focus` as you can focus an element in this environment)
                if selector is ct.Null:
                    continue
                # Verify tag matches
                if not self.match_tag(el, selector.tag):
                    continue
                # Verify tag is defined
                if selector.flags & ct.SEL_DEFINED and not self.match_defined(el):
                    continue
                # Verify element is root
                if selector.flags & ct.SEL_ROOT and not self.match_root(el):
                    continue
                # Verify element is scope
                if selector.flags & ct.SEL_SCOPE and not self.match_scope(el):
                    continue
                # Verify element has placeholder shown
                if selector.flags & ct.SEL_PLACEHOLDER_SHOWN and not self.match_placeholder_shown(el):
                    continue
                # Verify element is an editing host or editable
                if selector.flags & ct.SEL_EDITABLE and not self.match_editable(el):
                    continue
                # Verify `nth` matches
                if selector.nth and not (yield from self.match_nth(el, selector.nth)):
                    continue
                if selector.flags & ct.SEL_EMPTY and not self.match_empty(el):
                    continue
                # Verify id matches
                if selector.ids and not self.match_id(el, selector.ids):
                    continue
                # Verify classes match
                if selector.classes and not self.match_classes(el, selector.classes):
                    continue
                # Verify attribute(s) match
                if not self.match_attributes(el, selector.attributes):
                    continue
                # Verify ranges
                if selector.flags & RANGES and not self.match_range(el, selector.flags & RANGES):
                    continue
                # Verify language patterns
                if selector.lang and not self.match_lang(el, selector.lang):
                    continue
                # Verify pseudo selector patterns
                if selector.selectors and not (yield from self.match_subselectors(el, selector.selectors)):
                    continue
                # Verify relationship selectors
                if selector.relation and not (yield from self.match_relations(el, selector.relation)):
                    continue
                # Validate that the current default selector match corresponds to the first submit button in the form
                if selector.flags & ct.SEL_DEFAULT and not self.match_default(el):
                    continue
                # Validate that the unset radio button is among radio buttons with the same name in a form that are
                # also not set.
                if selector.flags & ct.SEL_INDETERMINATE and not self.match_indeterminate(el):
                    continue
                # Validate that the radio button is left checked in its group or that the option is selected.
                if selector.flags & ct.SEL_CHECKED and not (
                    self.match_checked_option(el) if self.get_tag(el) == 'option' else self.match_checked_radio(el)
                ):
                    continue
                # Validate element directionality
                if selector.flags & DIR_FLAGS and not self.match_dir(el, selector.flags & DIR_FLAGS):
                    continue
                # Validate that the tag contains the specified text.
                if selector.contains and not self.match_contains(el, selector.contains):
                    continue
                match = not is_not
                break

        # Restore actual namespaces being used for external selector lists
        if is_html:
            self.namespaces = namespaces

        yield match

    def select(self, limit: int = 0) -> Iterator[bs4.Tag]:
        """Match all tags under the targeted tag."""

        lim = None if limit < 1 else limit

        # Selecting from a `template` is how its contents are reached, but only for that `template`.
        for child in self.get_tag_descendants(self.tag, template_contents=True):
            if self.match(child):
                yield child
                if lim is not None:
                    lim -= 1
                    if lim < 1:
                        break

    def closest(self) -> bs4.Tag | None:
        """Match closest ancestor."""

        current: bs4.Tag | None = self.tag
        closest = None
        while closest is None and current is not None:
            if self.match(current):
                closest = current
            else:
                current = self.get_parent(current)
        return closest

    def filter(self) -> list[bs4.Tag]:  # noqa A001
        """Filter tag's children."""

        return [
            tag for tag in self.get_contents(self.tag, template_contents=True)
            if isinstance(tag, bs4.Tag) and self.match(tag)
        ]

    def match(self, el: bs4.Tag) -> bool:
        """Match."""

        return not self.is_doc(el) and self.is_tag(el) and self.match_selectors(el, self.selectors)


class SoupSieve(ct.Immutable):
    """Compiled Soup Sieve selector matching object."""

    pattern: str
    selectors: ct.SelectorList
    namespaces: ct.Namespaces | None
    custom: dict[str, str]
    flags: int

    __slots__ = ("pattern", "selectors", "namespaces", "custom", "flags", "_hash")

    def __init__(
        self,
        pattern: str,
        selectors: ct.SelectorList,
        namespaces: ct.Namespaces | None,
        custom: ct.CustomSelectors | None,
        flags: int
    ):
        """Initialize."""

        super().__init__(
            pattern=pattern,
            selectors=selectors,
            namespaces=namespaces,
            custom=custom,
            flags=flags
        )

    def match(self, tag: bs4.Tag) -> bool:
        """Match."""

        return CSSMatch(self.selectors, tag, self.namespaces, self.flags).match(tag)

    def closest(self, tag: bs4.Tag) -> bs4.Tag | None:
        """Match closest ancestor."""

        return CSSMatch(self.selectors, tag, self.namespaces, self.flags).closest()

    def filter(self, iterable: Iterable[bs4.Tag]) -> list[bs4.Tag]:  # noqa A001
        """
        Filter.

        `CSSMatch` can cache certain searches for tags of the same document,
        so if we are given a tag, all tags are from the same document,
        and we can take advantage of the optimization.

        Any other kind of iterable could have tags from different documents or detached tags,
        so for those, we use a new `CSSMatch` for each item in the iterable.
        """

        if isinstance(iterable, bs4.Tag):
            return CSSMatch(self.selectors, iterable, self.namespaces, self.flags).filter()
        else:
            # There is no guarantee that elements are from the same document, evaluate them separately.
            return [node for node in iterable if not CSSMatch.is_navigable_string(node) and self.match(node)]

    def select_one(self, tag: bs4.Tag) -> bs4.Tag | None:
        """Select a single tag."""

        tags = self.select(tag, limit=1)
        return tags[0] if tags else None

    def select(self, tag: bs4.Tag, limit: int = 0) -> list[bs4.Tag]:
        """Select the specified tags."""

        return list(self.iselect(tag, limit))

    def iselect(self, tag: bs4.Tag, limit: int = 0) -> Iterator[bs4.Tag]:
        """Iterate the specified tags."""

        yield from CSSMatch(self.selectors, tag, self.namespaces, self.flags).select(limit)

    def __repr__(self) -> str:  # pragma: no cover
        """Representation."""

        return (
            f"SoupSieve(pattern={self.pattern!r}, namespaces={self.namespaces!r}, "
            f"custom={self.custom!r}, flags={self.flags!r})"
        )

    __str__ = __repr__


ct.pickle_register(SoupSieve)
