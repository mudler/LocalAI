"""CSS selector structure items."""
from __future__ import annotations
from enum import Enum
import copyreg
from .pretty import pretty
from . import util
from typing import Any, Iterator, Pattern, Iterable, Mapping, TypeVar, overload

__all__ = (
    'Selector',
    'SelectorNull',
    'SelectorTag',
    'SelectorAttribute',
    'SelectorContains',
    'SelectorNth',
    'SelectorLang',
    'SelectorList',
    'Namespaces',
    'CustomSelectors'
)

SEL_EMPTY = 0x1
SEL_ROOT = 0x2
SEL_DEFAULT = 0x4
SEL_INDETERMINATE = 0x8
SEL_SCOPE = 0x10
SEL_DIR_LTR = 0x20
SEL_DIR_RTL = 0x40
SEL_IN_RANGE = 0x80
SEL_OUT_OF_RANGE = 0x100
SEL_DEFINED = 0x200
SEL_PLACEHOLDER_SHOWN = 0x400
SEL_EDITABLE = 0x800
SEL_CHECKED = 0x1000


def _repr_expand(value: Any) -> bool:  # pragma: no cover
    """Check if the value is an immutable object that uses the default representation."""

    return isinstance(value, Immutable) and type(value).__repr__ is Immutable.__repr__


def _repr_value(value: Any) -> Any:  # pragma: no cover
    """
    Return the representation of a leaf value, or the value itself if it must be expanded.

    Immutable objects using the default representation are expanded, as are tuples containing them.
    """

    if _repr_expand(value) or (value.__class__ is tuple and any(_repr_expand(v) for v in value)):
        return value
    return repr(value)


class Immutable:
    """Immutable."""

    __slots__: tuple[str, ...] = ('_hash',)

    _hash: int

    def __init__(self, **kwargs: Any) -> None:
        """Initialize."""

        temp = []
        for k, v in kwargs.items():
            temp.append(type(v))
            temp.append(v)
            super().__setattr__(k, v)
        super().__setattr__('_hash', hash(tuple(temp)))

    @classmethod
    def __base__(cls) -> type[Immutable]:
        """Get base class."""

        return cls

    def __eq__(self, other: Any) -> bool:
        """Check equaling in a non-recursive manner."""

        stack: list[tuple[Immutable, Any]] = [(self, other)]
        while stack:
            a, b = stack.pop()
            if a is b:
                continue

            # The hash is derived from the content, so differing hashes cannot be equal.
            if not isinstance(b, a.__base__()) or a._hash != b._hash:
                return False

            # Compare the attributes in the slot
            for key in a.__slots__[:-1]:
                x = getattr(a, key)
                y = getattr(b, key)
                if isinstance(x, Immutable):
                    stack.append((x, y))
                elif x.__class__ is tuple and y.__class__ is tuple:
                    if len(x) != len(y):  # pragma: no cover
                        return False
                    for i, j in zip(x, y, strict=True):
                        if isinstance(i, Immutable):
                            stack.append((i, j))
                        elif i != j:  # pragma: no cover
                            return False
                elif x != y:
                    return False
        return True

    def __ne__(self, other: Any) -> bool:
        """Not equal."""

        return not self.__eq__(other)

    def __hash__(self) -> int:
        """Hash."""

        return self._hash

    def __setattr__(self, name: str, value: Any) -> None:
        """Prevent mutability."""

        raise AttributeError(f"'{self.__class__.__name__}' is immutable")

    def __repr__(self) -> str:  # pragma: no cover
        """
        Build a representation without using recursion.

        Selectors can be nested very deeply, so build the representation of nested
        immutable objects with an explicit stack instead of recursing into their `__repr__`.
        Strings on the stack are output as is, other items are expanded into more items.
        """

        output: list[str] = []
        stack: list[Any] = [self]
        while stack:
            item = stack.pop()
            if item.__class__ is str:
                output.append(item)
            elif isinstance(item, Immutable):
                # Push parts in reverse so they are popped in order: `Name(key=value, ...)`
                stack.append(')')
                keys = item.__slots__[:-1]
                for index in range(len(keys) - 1, -1, -1):
                    stack.append(_repr_value(getattr(item, keys[index])))
                    stack.append(f'{keys[index]}=' if index == 0 else f', {keys[index]}=')
                stack.append(f'{item.__class__.__name__}(')
            else:
                # A tuple containing immutable objects: `(a,)` or `(a, b, ...)`
                stack.append(',)' if len(item) == 1 else ')')
                for index in range(len(item) - 1, -1, -1):
                    stack.append(_repr_value(item[index]))
                    if index:
                        stack.append(', ')
                stack.append('(')
        return ''.join(output)

    __str__ = __repr__

    def pretty(self) -> None:  # pragma: no cover
        """Pretty print."""

        print(pretty(self))


class Selector(Immutable):
    """Selector."""

    __slots__ = (
        'tag', 'ids', 'classes', 'attributes', 'nth', 'selectors',
        'relation', 'rel_type', 'contains', 'lang', 'flags', '_hash'
    )

    tag: SelectorTag | None
    ids: tuple[str, ...]
    classes: tuple[str, ...]
    attributes: tuple[SelectorAttribute, ...]
    nth: tuple[SelectorNth, ...]
    selectors: tuple[SelectorList, ...]
    relation: SelectorList
    rel_type: str | None
    contains: tuple[SelectorContains, ...]
    lang: tuple[SelectorLang, ...]
    flags: int

    def __init__(
        self,
        tag: SelectorTag | None,
        ids: tuple[str, ...],
        classes: tuple[str, ...],
        attributes: tuple[SelectorAttribute, ...],
        nth: tuple[SelectorNth, ...],
        selectors: tuple[SelectorList, ...],
        relation: SelectorList,
        rel_type: str | None,
        contains: tuple[SelectorContains, ...],
        lang: tuple[SelectorLang, ...],
        flags: int
    ):
        """Initialize."""

        super().__init__(
            tag=tag,
            ids=ids,
            classes=classes,
            attributes=attributes,
            nth=nth,
            selectors=selectors,
            relation=relation,
            rel_type=rel_type,
            contains=contains,
            lang=lang,
            flags=flags
        )


class SelectorNull(Enum):
    """Null Selector."""

    null = 0


Null = SelectorNull.null


class SelectorTag(Immutable):
    """Selector tag."""

    __slots__ = ("name", "prefix", "_hash")

    name: str
    prefix: str | None

    def __init__(self, name: str, prefix: str | None) -> None:
        """Initialize."""

        super().__init__(name=name, prefix=prefix)


class SelectorAttribute(Immutable):
    """Selector attribute rule."""

    __slots__ = ("attribute", "prefix", "pattern", "xml_type_pattern", "inverse", "ignore_case", "_hash")

    attribute: str
    prefix: str
    pattern: Pattern[str] | None
    xml_type_pattern: Pattern[str] | None
    inverse: bool
    ignore_case: bool

    def __init__(
        self,
        attribute: str,
        prefix: str,
        pattern: Pattern[str] | None,
        xml_type_pattern: Pattern[str] | None,
        inverse: bool,
        ignore_case: bool = False
    ) -> None:
        """
        Initialize.

        When `ignore_case` is set, `pattern` matches the attribute value converted to ASCII lowercase.
        `xml_type_pattern` is always case sensitive.
        """

        super().__init__(
            attribute=attribute,
            prefix=prefix,
            pattern=pattern,
            xml_type_pattern=xml_type_pattern,
            inverse=inverse,
            ignore_case=ignore_case
        )


class SelectorContains(Immutable):
    """Selector contains rule."""

    __slots__ = ("text", "own", "_hash")

    text: tuple[str, ...]
    own: bool

    def __init__(self, text: Iterable[str], own: bool) -> None:
        """Initialize."""

        super().__init__(text=tuple(text), own=own)


class SelectorNth(Immutable):
    """Selector nth type."""

    __slots__ = ("a", "n", "b", "of_type", "last", "selectors", "_hash")

    a: int
    n: bool
    b: int
    of_type: bool
    last: bool
    selectors: SelectorList

    def __init__(self, a: int, n: bool, b: int, of_type: bool, last: bool, selectors: SelectorList) -> None:
        """Initialize."""

        super().__init__(
            a=a,
            n=n,
            b=b,
            of_type=of_type,
            last=last,
            selectors=selectors
        )


class SelectorLang(Immutable):
    """Selector language rules."""

    __slots__ = ("languages", "_hash",)

    languages: tuple[str, ...]

    def __init__(self, languages: Iterable[str]):
        """Initialize."""

        super().__init__(languages=tuple(languages))

    def __iter__(self) -> Iterator[str]:
        """Iterator."""

        return iter(self.languages)

    def __len__(self) -> int:  # pragma: no cover
        """Length."""

        return len(self.languages)

    def __getitem__(self, index: int) -> str:  # pragma: no cover
        """Get item."""

        return self.languages[index]


class SelectorList(Immutable):
    """Selector list."""

    __slots__ = ("selectors", "is_not", "is_html", "count", "_hash")

    selectors: tuple[Selector | SelectorNull, ...]
    is_not: bool
    is_html: bool
    count: int

    def __init__(
        self,
        selectors: Iterable[Selector | SelectorNull] | None = None,
        is_not: bool = False,
        is_html: bool = False,
        count: int = 0,
    ) -> None:
        """Initialize."""

        super().__init__(
            selectors=tuple(selectors) if selectors is not None else (),
            is_not=is_not,
            is_html=is_html,
            count=count
        )

    def __iter__(self) -> Iterator[Selector | SelectorNull]:
        """Iterator."""

        return iter(self.selectors)

    def __len__(self) -> int:
        """Length."""

        return len(self.selectors)

    def __getitem__(self, index: int) -> Selector | SelectorNull:
        """Get item."""

        return self.selectors[index]


KT = TypeVar('KT')
VT = TypeVar('VT')
RT = TypeVar('RT')


class ImmutableDict(Mapping[KT, VT]):
    """Hashable, immutable dictionary."""

    def __init__(
        self,
        arg: Mapping[KT, VT]
    ) -> None:
        """Initialize."""

        self._d = dict(arg)
        self._hash = hash(tuple([(type(x), x, type(y), y) for x, y in sorted(self._d.items())]))

    @overload
    def get(self, key: KT, /) -> VT | None:
        ...

    @overload
    def get(self, key: KT, /, default: VT) -> VT:
        ...

    @overload
    def get(self, key: KT, /, default: RT) -> VT | RT:
        ...

    def get(self, key: KT, /, default: RT | VT | None = None) -> VT | RT | None:
        """Get value."""

        return self._d.get(key, default)

    def __iter__(self) -> Iterator[KT]:
        """Iterator."""

        return iter(self._d)

    def __len__(self) -> int:
        """Length."""

        return len(self._d)

    def __getitem__(self, key: KT) -> VT:
        """Get item: `namespace['key']`."""

        return self._d[key]

    def __hash__(self) -> int:
        """Hash."""

        return self._hash

    def __repr__(self) -> str:  # pragma: no cover
        """Representation."""

        return f"{self._d!r}"

    __str__ = __repr__


class Namespaces(ImmutableDict[str, str]):
    """Namespaces."""

    def __init__(self, arg: Mapping[str, str]) -> None:
        """Initialize."""

        self._validate(arg)
        super().__init__(arg)

    def _validate(self, arg: Mapping[str, str]) -> None:
        """Validate arguments."""

        if not all(isinstance(k, str) and isinstance(v, str) for k, v in arg.items()):
            raise TypeError(f'{self.__class__.__name__} keys and values must be strings')


class CustomSelectors(ImmutableDict[str, SelectorList]):
    """Custom selectors."""

    def __init__(self, arg: Mapping[str, str | SelectorList], max_selectors: int | None = None) -> None:
        """Initialize."""

        from . import css_parser as cp

        if max_selectors is None:
            max_selectors = cp.SELECTOR_LIMIT

        self._d = {}
        for k, v in arg.items():
            if not isinstance(k, str):
                raise TypeError(f'{self.__class__.__name__} keys must be strings')
            # Validate key
            k = util.lower(cp.css_unescape(k))
            if k in self._d:
                raise KeyError(f'Custom selector {k} was defined twice')
            if cp.RE_CUSTOM.match(k) is None:
                raise cp.SelectorSyntaxError(f"The name '{k}' is not a valid custom pseudo-class name")
            # Compile value
            if isinstance(v, str):
                v = cp.CSSParser(v, max_selectors=max_selectors, custom=self._d).process_selectors(flags=cp.FLG_PSEUDO)
            elif not isinstance(v, SelectorList):
                raise TypeError(f"Custom selector values must be either 'str' or 'SelectorList', not {type(v)}")
            # Monitor count
            max_selectors -= v.count
            self._d[k] = v
        self._hash = hash(tuple([(x, y) for x, y in sorted(self._d.items())] + [max_selectors]))


def _pickle(p: Immutable) -> Any:
    """
    Reduce an immutable object for pickling.

    Selectors can be nested very deeply, and pickling them object by object would
    recurse for each level. Instead, flatten the entire structure into a table of
    nodes, children before parents, where nested immutable objects are replaced by
    their index in the table. As the table only contains leaf values, neither
    pickling nor unpickling needs to recurse.

    Each node is `(class, args, refs)`. A ref is either the index of an argument that
    is an immutable object, or `(index, positions)` for an argument that is a tuple
    containing immutable objects at the given positions.
    """

    indexes: dict[int, int] = {}
    nodes: list[tuple[type[Immutable], tuple[Any, ...], tuple[Any, ...]]] = []
    stack: list[tuple[Immutable, list[Any] | None]] = [(p, None)]

    while stack:
        obj, values = stack.pop()
        if id(obj) in indexes:
            continue

        # Queue up children to be processed before the parent.
        if values is None:
            values = [getattr(obj, s) for s in obj.__slots__[:-1]]
            stack.append((obj, values))
            for value in values:
                if isinstance(value, Immutable):
                    stack.append((value, None))
                elif value.__class__ is tuple:
                    stack.extend((v, None) for v in value if isinstance(v, Immutable))
            continue

        # All children have been processed, replace them with their index.
        refs: list[Any] = []
        for i, value in enumerate(values):
            if isinstance(value, Immutable):
                values[i] = indexes[id(value)]
                refs.append(i)
            elif value.__class__ is tuple:
                positions = [j for j, v in enumerate(value) if isinstance(v, Immutable)]
                if positions:
                    items = list(value)
                    for j in positions:
                        items[j] = indexes[id(items[j])]
                    values[i] = tuple(items)
                    refs.append((i, tuple(positions)))

        indexes[id(obj)] = len(nodes)
        nodes.append((obj.__base__(), tuple(values), tuple(refs)))

    return _unpickle, (tuple(nodes),)


def _unpickle(nodes: tuple[tuple[type[Immutable], tuple[Any, ...], tuple[Any, ...]], ...]) -> Immutable:
    """Rebuild an immutable object from the flattened node table created by `_pickle`."""

    objs: list[Immutable] = []
    for cls, args, refs in nodes:
        values = list(args)
        for ref in refs:
            if isinstance(ref, int):
                values[ref] = objs[values[ref]]
            else:
                i, positions = ref
                items = list(values[i])
                for j in positions:
                    items[j] = objs[items[j]]
                values[i] = tuple(items)
        objs.append(cls(*values))
    return objs[-1]


def pickle_register(obj: Any) -> None:
    """Allow object to be pickled."""

    copyreg.pickle(obj, _pickle)


pickle_register(Selector)
pickle_register(SelectorTag)
pickle_register(SelectorAttribute)
pickle_register(SelectorContains)
pickle_register(SelectorNth)
pickle_register(SelectorLang)
pickle_register(SelectorList)
