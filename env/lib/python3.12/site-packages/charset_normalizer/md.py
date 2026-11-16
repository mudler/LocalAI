from __future__ import annotations

from functools import lru_cache
from logging import getLogger


from .constant import (
    COMMON_CJK_CHARACTERS,
    COMMON_SAFE_ASCII_CHARACTERS,
    TRACE,
    CompatibleFamillyRange,
    _ACCENTUATED,
    _ARABIC,
    _ARABIC_ISOLATED_FORM,
    _BASIC_LATIN_COMPATIBLE_RANGE_FAMILIES,
    _CJK,
    _COMPATIBLE_RANGE_FAMILIES,
    _COMPATIBLE_WITH_ANY_RANGE_FAMILIES,
    _HANGUL,
    _HALFWIDTH_KATAKANA,
    _HIRAGANA,
    _KATAKANA,
    _LATIN,
    _LIGATURE,
    _RANGE_FAMILIES,
    _SENTENCE_OPEN_PUNCTUATION,
    _SUPERSCRIPT,
    _THAI,
)
from .utils import (
    _character_flags,
    is_emoticon,
    is_punctuation,
    is_separator,
    is_symbol,
    remove_accent,
    unicode_range,
)

# Combined bitmask for CJK/Hangul/Katakana/Hiragana/Thai glyph detection.
_GLYPH_MASK: int = _CJK | _HANGUL | _KATAKANA | _HIRAGANA | _THAI


class CharInfo:
    """Pre-computed character properties shared across all detectors."""

    __slots__ = (
        "character",
        "printable",
        "alpha",
        "upper",
        "lower",
        "space",
        "digit",
        "is_ascii",
        "case_variable",
        "flags",
        "accentuated",
        "latin",
        "is_cjk",
        "is_katakana",
        "is_halfwidth_katakana",
        "is_arabic",
        "is_ligature",
        "is_superscript",
        "is_sentence_open_punctuation",
        "is_glyph",
        "punct",
        "sym",
        "range",
        "sep",
        "emoticon",
        "safe",
        "common_cjk",
        "unaccented",
    )

    character: str
    printable: bool
    alpha: bool
    upper: bool
    lower: bool
    space: bool
    digit: bool
    is_ascii: bool
    case_variable: bool
    flags: int
    accentuated: bool
    latin: bool
    is_cjk: bool
    is_katakana: bool
    is_halfwidth_katakana: bool
    is_arabic: bool
    is_ligature: bool
    is_superscript: bool
    is_sentence_open_punctuation: bool
    is_glyph: bool
    punct: bool
    sym: bool
    range: str | None
    sep: bool
    emoticon: bool
    safe: bool
    common_cjk: bool
    unaccented: str

    def __init__(self, character: str) -> None:
        """Compute all properties for *character* (built once per codepoint,
        every branch assigns every slot)."""
        self.character = character

        # ASCII fast-path: for characters with ord < 128, we can skip
        # _character_flags() entirely and derive most properties from ord.
        o: int = ord(character)
        if o < 128:
            self.is_ascii = True
            self.accentuated = False
            self.unaccented = character
            self.emoticon = False
            self.common_cjk = False
            self.safe = character in COMMON_SAFE_ASCII_CHARACTERS
            self.is_cjk = False
            self.is_katakana = False
            self.is_halfwidth_katakana = False
            self.is_arabic = False
            self.is_ligature = False
            self.is_superscript = False
            self.is_sentence_open_punctuation = False
            self.is_glyph = False
            # ASCII alpha: a-z (97-122) or A-Z (65-90)
            if 65 <= o <= 90:
                # Uppercase ASCII letter
                self.alpha = True
                self.upper = True
                self.lower = False
                self.space = False
                self.digit = False
                self.printable = True
                self.case_variable = True
                self.flags = _LATIN
                self.latin = True
                self.punct = False
                self.sym = False
            elif 97 <= o <= 122:
                # Lowercase ASCII letter
                self.alpha = True
                self.upper = False
                self.lower = True
                self.space = False
                self.digit = False
                self.printable = True
                self.case_variable = True
                self.flags = _LATIN
                self.latin = True
                self.punct = False
                self.sym = False
            elif 48 <= o <= 57:
                # ASCII digit 0-9
                self.alpha = False
                self.upper = False
                self.lower = False
                self.space = False
                self.digit = True
                self.printable = True
                self.case_variable = False
                self.flags = 0
                self.latin = False
                self.punct = False
                self.sym = False
            elif o == 32 or (9 <= o <= 13):
                # Space, tab, newline, etc.
                self.alpha = False
                self.upper = False
                self.lower = False
                self.space = True
                self.digit = False
                self.printable = o == 32
                self.case_variable = False
                self.flags = 0
                self.latin = False
                self.punct = False
                self.sym = False
            else:
                # Other ASCII (punctuation, symbols, control chars)
                self.printable = character.isprintable()
                self.alpha = False
                self.upper = False
                self.lower = False
                self.space = False
                self.digit = False
                self.case_variable = False
                self.flags = 0
                self.latin = False
                self.punct = is_punctuation(character) if self.printable else False
                self.sym = is_symbol(character) if self.printable else False
        else:
            # Non-ASCII path
            self.is_ascii = False
            self.safe = False
            self.printable = character.isprintable()
            self.alpha = character.isalpha()
            self.upper = character.isupper()
            self.lower = character.islower()
            self.space = character.isspace()
            self.digit = character.isdigit()
            self.case_variable = self.lower != self.upper

            # Flag-based classification (single unicodedata.name() call, lru-cached)
            flags: int = _character_flags(character)
            if self.alpha:
                self.emoticon = False
            else:
                self.emoticon = is_emoticon(character)
            self.flags = flags
            self.accentuated = bool(flags & _ACCENTUATED)
            self.latin = bool(flags & _LATIN)
            self.is_cjk = bool(flags & _CJK)
            self.is_katakana = bool(flags & _KATAKANA)
            self.is_halfwidth_katakana = bool(flags & _HALFWIDTH_KATAKANA)
            self.is_arabic = bool(flags & _ARABIC)
            self.is_ligature = bool(flags & _LIGATURE)
            self.is_superscript = bool(flags & _SUPERSCRIPT)
            self.is_sentence_open_punctuation = bool(flags & _SENTENCE_OPEN_PUNCTUATION)
            self.is_glyph = bool(flags & _GLYPH_MASK)

            if self.latin and self.accentuated:
                self.unaccented = remove_accent(character)
            else:
                self.unaccented = character

            self.common_cjk = self.is_cjk and character in COMMON_CJK_CHARACTERS

            # Eagerly compute punct and sym (avoids property dispatch overhead
            # on 300K+ accesses in the hot loop).
            if self.printable:
                self.punct = is_punctuation(character)
                self.sym = is_symbol(character)
            else:
                self.punct = False
                self.sym = False

        self.range = unicode_range(character)
        self.sep = is_separator(character)


# Per-codepoint cache of CharInfo instances
# At most UTF-8 size allocated.
@lru_cache(maxsize=None)
def _char_info(character: str) -> CharInfo:
    """Build (once per codepoint) and cache the CharInfo for *character*."""
    return CharInfo(character)


# ASCII table indexed by codepoint.
_ASCII_CHAR_INFO: list[CharInfo] = [
    CharInfo(chr(_codepoint)) for _codepoint in range(128)
]


class MessDetectorPlugin:
    """
    Base abstract class used for mess detection plugins.
    All detectors MUST extend and implement given methods.
    """

    __slots__ = ()

    def feed_info(self, character: str, info: CharInfo) -> None:
        """
        The main routine to be executed upon character.
        Insert the logic in witch the text would be considered chaotic.
        """
        raise NotImplementedError  # Defensive:

    def reset(self) -> None:  # Defensive:
        """
        Permit to reset the plugin to the initial state.
        """
        raise NotImplementedError

    @property
    def ratio(self) -> float:
        """
        Compute the chaos ratio based on what your feed() has seen.
        Must NOT be lower than 0.; No restriction gt 0.
        """
        raise NotImplementedError  # Defensive:


class TooManySymbolOrPunctuationPlugin(MessDetectorPlugin):
    __slots__ = (
        "_punctuation_count",
        "_symbol_count",
        "_character_count",
        "_last_printable_char",
    )

    def __init__(self) -> None:
        self._punctuation_count: int = 0
        self._symbol_count: int = 0
        self._character_count: int = 0

        self._last_printable_char: str | None = None

    def feed_info(self, character: str, info: CharInfo) -> None:
        """Optimized feed using pre-computed character info."""
        self._character_count += 1

        if character != self._last_printable_char and not info.safe:
            if info.punct:
                self._punctuation_count += 1
            elif not info.digit and info.sym and not info.emoticon:
                self._symbol_count += 2

        self._last_printable_char = character

    def reset(self) -> None:  # Abstract
        self._punctuation_count = 0
        self._character_count = 0
        self._symbol_count = 0

    @property
    def ratio(self) -> float:
        if self._character_count == 0:
            return 0.0

        ratio_of_punctuation: float = (
            self._punctuation_count + self._symbol_count
        ) / self._character_count

        return ratio_of_punctuation if ratio_of_punctuation >= 0.3 else 0.0


class TooManyAccentuatedPlugin(MessDetectorPlugin):
    __slots__ = ("_character_count", "_accentuated_count")

    def __init__(self) -> None:
        self._character_count: int = 0
        self._accentuated_count: int = 0

    def feed_info(self, character: str, info: CharInfo) -> None:
        """Optimized feed using pre-computed character info."""
        self._character_count += 1

        if info.accentuated:
            self._accentuated_count += 1

    def reset(self) -> None:  # Abstract
        self._character_count = 0
        self._accentuated_count = 0

    @property
    def ratio(self) -> float:
        if self._character_count < 8:
            return 0.0

        ratio_of_accentuation: float = self._accentuated_count / self._character_count
        return ratio_of_accentuation if ratio_of_accentuation >= 0.35 else 0.0


class UnprintablePlugin(MessDetectorPlugin):
    __slots__ = ("_unprintable_count", "_character_count", "_has_escape")

    def __init__(self) -> None:
        self._unprintable_count: int = 0
        self._character_count: int = 0
        self._has_escape: bool = False

    def feed_info(self, character: str, info: CharInfo) -> None:
        """Optimized feed using pre-computed character info."""
        if character == "\x1b":
            self._has_escape = True

        if (
            not info.printable
            and not info.space
            and character != "\x1a"
            and character != "\ufeff"
        ):
            self._unprintable_count += 1
        self._character_count += 1

    def reset(self) -> None:  # Abstract
        self._unprintable_count = 0
        self._has_escape = False

    @property
    def ratio(self) -> float:
        if self._character_count == 0:  # Defensive:
            return 0.0

        if self._has_escape:
            return 1.0

        return (self._unprintable_count * 8) / self._character_count


class SuspiciousDuplicateAccentPlugin(MessDetectorPlugin):
    __slots__ = (
        "_successive_count",
        "_character_count",
        "_last_latin_character",
        "_last_was_accentuated",
    )

    def __init__(self) -> None:
        self._successive_count: int = 0
        self._character_count: int = 0

        self._last_latin_character: CharInfo | None = None
        self._last_was_accentuated: bool = False

    def feed_info(self, character: str, info: CharInfo) -> None:
        """Optimized feed using pre-computed character info."""
        self._character_count += 1
        if (
            self._last_latin_character is not None
            and info.accentuated
            and self._last_was_accentuated
        ):
            if info.upper and self._last_latin_character.upper:
                self._successive_count += 1
            if info.unaccented == self._last_latin_character.unaccented:
                self._successive_count += 1
        self._last_latin_character = info
        self._last_was_accentuated = info.accentuated

    def reset(self) -> None:  # Abstract
        self._successive_count = 0
        self._character_count = 0
        self._last_latin_character = None
        self._last_was_accentuated = False

    @property
    def ratio(self) -> float:
        if self._character_count == 0:
            return 0.0

        return (self._successive_count * 2) / self._character_count


class SuspiciousRange(MessDetectorPlugin):
    __slots__ = (
        "_suspicious_successive_range_count",
        "_character_count",
        "_last_printable_seen",
        "_last_printable_range",
    )

    def __init__(self) -> None:
        self._suspicious_successive_range_count: int = 0
        self._character_count: int = 0
        self._last_printable_seen: str | None = None
        self._last_printable_range: str | None = None

    def feed_info(self, character: str, info: CharInfo) -> None:
        """Optimized feed using pre-computed character info."""
        self._character_count += 1

        if info.space or info.punct or info.safe:
            self._last_printable_seen = None
            self._last_printable_range = None
            return

        if self._last_printable_seen is None:
            self._last_printable_seen = character
            self._last_printable_range = info.range
            return

        unicode_range_a: str | None = self._last_printable_range
        unicode_range_b: str | None = info.range

        # Identical non-None ranges can never be suspicious.
        if unicode_range_a != unicode_range_b or unicode_range_a is None:
            if is_suspiciously_successive_range(unicode_range_a, unicode_range_b):
                self._suspicious_successive_range_count += 1

        self._last_printable_seen = character
        self._last_printable_range = unicode_range_b

    def reset(self) -> None:  # Abstract
        self._character_count = 0
        self._suspicious_successive_range_count = 0
        self._last_printable_seen = None
        self._last_printable_range = None

    @property
    def ratio(self) -> float:
        if self._character_count <= 13:
            return 0.0

        ratio_of_suspicious_range_usage: float = (
            self._suspicious_successive_range_count * 2
        ) / self._character_count

        return ratio_of_suspicious_range_usage


class SuperWeirdWordPlugin(MessDetectorPlugin):
    __slots__ = (
        "_word_count",
        "_bad_word_count",
        "_foreign_long_count",
        "_is_current_word_bad",
        "_foreign_long_watch",
        "_character_count",
        "_bad_character_count",
        "_buffer_length",
        "_buffer_last_char",
        "_buffer_last_char_accentuated",
        "_buffer_accent_count",
        "_buffer_glyph_count",
        "_buffer_upper_count",
        "_buffer_first_lower",
        "_buffer_has_non_ascii",
        "_buffer_last_char_ligature",
        "_buffer_has_internal_ligature",
        "_is_current_word_invalid",
        "_invalid_word_count",
    )

    def __init__(self) -> None:
        self._word_count: int = 0
        self._bad_word_count: int = 0
        self._foreign_long_count: int = 0

        self._is_current_word_bad: bool = False
        self._foreign_long_watch: bool = False

        self._character_count: int = 0
        self._bad_character_count: int = 0

        self._buffer_length: int = 0
        self._buffer_last_char: str | None = None
        self._buffer_last_char_accentuated: bool = False
        self._buffer_accent_count: int = 0
        self._buffer_glyph_count: int = 0
        self._buffer_upper_count: int = 0
        self._buffer_first_lower: bool = False
        self._buffer_has_non_ascii: bool = False
        self._buffer_last_char_ligature: bool = False
        self._buffer_has_internal_ligature: bool = False
        self._is_current_word_invalid: bool = False
        self._invalid_word_count: int = 0

    def feed_info(self, character: str, info: CharInfo) -> None:
        """Optimized feed using pre-computed character info."""
        if info.alpha:
            if self._buffer_last_char_ligature:
                self._buffer_has_internal_ligature = True
            self._buffer_last_char_ligature = info.is_ligature
            if self._buffer_length == 0:
                self._buffer_first_lower = info.lower
            self._buffer_length += 1
            self._buffer_last_char = character

            if info.upper:
                self._buffer_upper_count += 1
            if not info.is_ascii:
                self._buffer_has_non_ascii = True

            self._buffer_last_char_accentuated = info.accentuated

            if info.accentuated:
                self._buffer_accent_count += 1
            if info.is_glyph:
                self._buffer_glyph_count += 1
            elif not self._foreign_long_watch and (not info.latin or info.accentuated):
                self._foreign_long_watch = True
            return
        if not self._buffer_length:
            return
        if info.is_sentence_open_punctuation or (
            info.is_superscript and self._buffer_has_internal_ligature
        ):
            self._is_current_word_bad = True
            self._is_current_word_invalid = True
        if info.space or info.punct or info.sep:
            self._word_count += 1
            buffer_length: int = self._buffer_length

            self._character_count += buffer_length

            if buffer_length >= 4:
                if self._buffer_accent_count / buffer_length >= 0.5:
                    self._is_current_word_bad = True
                elif (
                    self._buffer_last_char_accentuated
                    and self._buffer_last_char.isupper()  # type: ignore[union-attr]
                    and self._buffer_upper_count != buffer_length
                ):
                    self._foreign_long_count += 1
                    self._is_current_word_bad = True
                elif self._buffer_glyph_count == 1:
                    self._is_current_word_bad = True
                    self._foreign_long_count += 1
                elif (
                    self._buffer_has_non_ascii
                    and self._buffer_first_lower
                    and self._buffer_upper_count == buffer_length - 1
                ):
                    # Inverse capitalization detector.
                    # No natural writing produces such words.
                    # see https://github.com/jawah/charset_normalizer/issues/731
                    self._foreign_long_count += 1
                    self._is_current_word_bad = True
            if buffer_length >= 24 and self._foreign_long_watch:
                probable_camel_cased: bool = (
                    self._buffer_upper_count > 0
                    and self._buffer_upper_count / buffer_length <= 0.3
                )

                if not probable_camel_cased:
                    self._foreign_long_count += 1
                    self._is_current_word_bad = True

            if self._is_current_word_bad:
                self._bad_word_count += 1
                self._bad_character_count += buffer_length
                self._is_current_word_bad = False
            if self._is_current_word_invalid:
                self._invalid_word_count += 1
                self._is_current_word_invalid = False

            self._foreign_long_watch = False
            self._buffer_length = 0
            self._buffer_last_char = None
            self._buffer_last_char_accentuated = False
            self._buffer_accent_count = 0
            self._buffer_glyph_count = 0
            self._buffer_upper_count = 0
            self._buffer_first_lower = False
            self._buffer_has_non_ascii = False
            self._buffer_last_char_ligature = False
            self._buffer_has_internal_ligature = False
        elif (
            character not in {"<", ">", "-", "=", "~", "|", "_"}
            and not info.digit
            and info.sym
        ):
            self._is_current_word_bad = True
            self._buffer_length += 1
            self._buffer_last_char = character
            self._buffer_last_char_accentuated = False

    def reset(self) -> None:  # Abstract
        self._buffer_length = 0
        self._buffer_last_char = None
        self._buffer_last_char_accentuated = False
        self._is_current_word_bad = False
        self._foreign_long_watch = False
        self._bad_word_count = 0
        self._word_count = 0
        self._character_count = 0
        self._bad_character_count = 0
        self._foreign_long_count = 0
        self._buffer_accent_count = 0
        self._buffer_glyph_count = 0
        self._buffer_upper_count = 0
        self._buffer_first_lower = False
        self._buffer_has_non_ascii = False
        self._buffer_last_char_ligature = False
        self._buffer_has_internal_ligature = False
        self._is_current_word_invalid = False
        self._invalid_word_count = 0

    @property
    def ratio(self) -> float:
        if self._invalid_word_count:
            return 1.0

        if self._word_count <= 10 and self._foreign_long_count == 0:
            return 0.0

        return self._bad_character_count / self._character_count


class CjkUncommonPlugin(MessDetectorPlugin):
    """
    Detect messy CJK text that probably means nothing.
    """

    __slots__ = ("_character_count", "_uncommon_count")

    def __init__(self) -> None:
        self._character_count: int = 0
        self._uncommon_count: int = 0

    def feed_info(self, character: str, info: CharInfo) -> None:
        """Optimized feed using pre-computed character info."""
        self._character_count += 1

        if not info.common_cjk:
            self._uncommon_count += 1

    def reset(self) -> None:  # Abstract
        self._character_count = 0
        self._uncommon_count = 0

    @property
    def ratio(self) -> float:
        if self._character_count < 4:
            return 0.0

        uncommon_form_usage: float = (
            2 * self._uncommon_count - self._character_count
        ) / (5 * max(self._character_count, 16))

        # we can be pretty sure it's garbage when uncommon characters are widely
        # used. otherwise it could just be traditional chinese for example.
        return max(0.0, uncommon_form_usage)


class SuspiciousKatakanaPlugin(MessDetectorPlugin):
    """Detect implausible halfwidth Katakana and uncommon CJK combinations."""

    __slots__ = (
        "_katakana_count",
        "_halfwidth_katakana_count",
        "_cjk_count",
        "_uncommon_cjk_count",
    )

    def __init__(self) -> None:
        self._katakana_count: int = 0
        self._halfwidth_katakana_count: int = 0
        self._cjk_count: int = 0
        self._uncommon_cjk_count: int = 0

    def feed_info(self, character: str, info: CharInfo) -> None:
        """Optimized feed using pre-computed character info."""
        if info.is_katakana:
            self._katakana_count += 1
            if info.is_halfwidth_katakana:
                self._halfwidth_katakana_count += 1
            return

        self._cjk_count += 1
        if not info.common_cjk:
            self._uncommon_cjk_count += 1

    def reset(self) -> None:  # Abstract
        self._katakana_count = 0
        self._halfwidth_katakana_count = 0
        self._cjk_count = 0
        self._uncommon_cjk_count = 0

    @property
    def ratio(self) -> float:
        if (
            self._halfwidth_katakana_count >= 4
            and self._halfwidth_katakana_count == self._katakana_count
            and 3 <= self._cjk_count == self._uncommon_cjk_count
        ):
            return 1.0

        return 0.0


class ArchaicUpperLowerPlugin(MessDetectorPlugin):
    __slots__ = (
        "_buf",
        "_character_count_since_last_sep",
        "_successive_upper_lower_count",
        "_successive_upper_lower_count_final",
        "_character_count",
        "_last_alpha_seen_upper",
        "_last_alpha_seen_lower",
        "_current_ascii_only",
    )

    def __init__(self) -> None:
        self._buf: bool = False

        self._character_count_since_last_sep: int = 0

        self._successive_upper_lower_count: int = 0
        self._successive_upper_lower_count_final: int = 0

        self._character_count: int = 0

        self._last_alpha_seen_upper: bool = False
        self._last_alpha_seen_lower: bool = False
        self._current_ascii_only: bool = True

    def feed_info(self, character: str, info: CharInfo) -> None:
        """Optimized feed using pre-computed character info."""
        is_concerned: bool = info.alpha and info.case_variable
        chunk_sep: bool = not is_concerned

        if chunk_sep and self._character_count_since_last_sep > 0:
            if (
                self._character_count_since_last_sep <= 64
                and not info.digit
                and not self._current_ascii_only
            ):
                self._successive_upper_lower_count_final += (
                    self._successive_upper_lower_count
                )

            self._successive_upper_lower_count = 0
            self._character_count_since_last_sep = 0
            self._buf = False
            self._character_count += 1
            self._current_ascii_only = True

            return

        if self._current_ascii_only and not info.is_ascii:
            self._current_ascii_only = False

        if self._character_count_since_last_sep > 0:
            if (info.upper and self._last_alpha_seen_lower) or (
                info.lower and self._last_alpha_seen_upper
            ):
                if self._buf:
                    self._successive_upper_lower_count += 2
                    self._buf = False
                else:
                    self._buf = True
            else:
                self._buf = False

        self._character_count += 1
        self._character_count_since_last_sep += 1
        self._last_alpha_seen_upper = info.upper
        self._last_alpha_seen_lower = info.lower

    def reset(self) -> None:  # Abstract
        self._character_count = 0
        self._character_count_since_last_sep = 0
        self._successive_upper_lower_count = 0
        self._successive_upper_lower_count_final = 0
        self._last_alpha_seen_upper = False
        self._last_alpha_seen_lower = False
        self._buf = False
        self._current_ascii_only = True

    @property
    def ratio(self) -> float:
        if self._character_count == 0:  # Defensive:
            return 0.0

        return self._successive_upper_lower_count_final / self._character_count


class ArabicIsolatedFormPlugin(MessDetectorPlugin):
    __slots__ = ("_character_count", "_isolated_form_count")

    def __init__(self) -> None:
        self._character_count: int = 0
        self._isolated_form_count: int = 0

    def reset(self) -> None:  # Abstract
        self._character_count = 0
        self._isolated_form_count = 0

    def feed_info(self, character: str, info: CharInfo) -> None:
        """Optimized feed using pre-computed character info."""
        self._character_count += 1

        if info.flags & _ARABIC_ISOLATED_FORM:
            self._isolated_form_count += 1

    @property
    def ratio(self) -> float:
        if self._character_count < 8:
            return 0.0

        isolated_form_usage: float = self._isolated_form_count / self._character_count

        return isolated_form_usage


@lru_cache(maxsize=None)
def is_suspiciously_successive_range(
    unicode_range_a: str | None, unicode_range_b: str | None
) -> bool:
    """
    Determine if two Unicode ranges seen next to each other can be considered suspicious.
    """
    if unicode_range_a is None or unicode_range_b is None:
        return True

    familly_a: str = _RANGE_FAMILIES[unicode_range_a]
    familly_b: str = _RANGE_FAMILIES[unicode_range_b]

    if familly_a == familly_b:
        return False

    if (
        familly_a in _COMPATIBLE_WITH_ANY_RANGE_FAMILIES
        or familly_b in _COMPATIBLE_WITH_ANY_RANGE_FAMILIES
    ):
        return False

    if CompatibleFamillyRange(familly_a, familly_b) in _COMPATIBLE_RANGE_FAMILIES:
        return False

    # Basic Latin is commonly interspersed with East Asian scripts, but the
    # compatibility must not extend to every range in the Latin family.
    if unicode_range_a == "Basic Latin":
        return familly_b not in _BASIC_LATIN_COMPATIBLE_RANGE_FAMILIES

    if unicode_range_b == "Basic Latin":
        return familly_a not in _BASIC_LATIN_COMPATIBLE_RANGE_FAMILIES

    return True


def mess_ratio(
    decoded_sequence: str, maximum_threshold: float = 0.2, debug: bool = False
) -> float:
    """
    Compute a mess ratio given a decoded bytes sequence. The maximum threshold does stop the computation earlier.
    """

    seq_len: int = len(decoded_sequence)

    if seq_len < 511:
        step: int = 32
    elif seq_len < 1024:
        step = 64
    else:
        step = 128

    # str.isascii() is O(1) (the flag lives in the str header). Seven of the
    # ten detectors provably keep a 0.0 ratio on ASCII-only input and are
    # therefore not fed at all.
    is_pure_ascii: bool = decoded_sequence.isascii()

    # Cached per-codepoint character properties (see CharInfo). ASCII
    # characters resolve through the immutable import-time table; anything
    # else goes through the lru_cache-backed slow path.
    ascii_info = _ASCII_CHAR_INFO
    char_info = _char_info

    mean_mess_ratio: float
    info: CharInfo

    # Create each detector as a named local variable (unrolled from the generic loop).
    # This eliminates per-character iteration over the detector list and
    # per-character eligible() virtual dispatch, while keeping every plugin class
    # intact and fully readable.
    d_sp: TooManySymbolOrPunctuationPlugin = TooManySymbolOrPunctuationPlugin()
    d_ta: TooManyAccentuatedPlugin = TooManyAccentuatedPlugin()
    d_up: UnprintablePlugin = UnprintablePlugin()
    d_sda: SuspiciousDuplicateAccentPlugin = SuspiciousDuplicateAccentPlugin()
    d_sr: SuspiciousRange = SuspiciousRange()
    d_sw: SuperWeirdWordPlugin = SuperWeirdWordPlugin()
    d_cu: CjkUncommonPlugin = CjkUncommonPlugin()
    d_sk: SuspiciousKatakanaPlugin = SuspiciousKatakanaPlugin()
    d_au: ArchaicUpperLowerPlugin = ArchaicUpperLowerPlugin()
    d_ai: ArabicIsolatedFormPlugin = ArabicIsolatedFormPlugin()

    # Local references avoid repeated bound-method creation in the hot loop.
    d_sp_feed = d_sp.feed_info
    d_ta_feed = d_ta.feed_info
    d_up_feed = d_up.feed_info
    d_sda_feed = d_sda.feed_info
    d_sr_feed = d_sr.feed_info
    d_sw_feed = d_sw.feed_info
    d_cu_feed = d_cu.feed_info
    d_sk_feed = d_sk.feed_info
    d_au_feed = d_au.feed_info
    d_ai_feed = d_ai.feed_info

    for block_start in range(0, seq_len, step):
        for character in decoded_sequence[block_start : block_start + step]:
            # Character properties computed once per distinct codepoint
            # (shared across all plugins and all mess_ratio calls).
            # ord() doubles as the ASCII table index.
            codepoint: int = ord(character)
            if codepoint < 128:
                info = ascii_info[codepoint]
            else:
                info = char_info(character)

            # Detectors with eligible() == always True
            d_up_feed(character, info)
            d_sw_feed(character, info)

            if is_pure_ascii:
                # The seven remaining detectors provably stay at 0.0 (see above).
                if info.printable:
                    d_sp_feed(character, info)
                continue

            d_au_feed(character, info)

            # Detectors with eligible() == isprintable
            if info.printable:
                d_sp_feed(character, info)
                d_sr_feed(character, info)

            # Detectors with eligible() == isalpha
            if info.alpha:
                d_ta_feed(character, info)
                # SuspiciousDuplicateAccent: isalpha() and is_latin()
                if info.latin:
                    d_sda_feed(character, info)
                # CjkUncommon and SuspiciousKatakana: is_cjk()
                if info.is_cjk:
                    d_cu_feed(character, info)
                    d_sk_feed(character, info)
                elif info.is_katakana:
                    d_sk_feed(character, info)
                # ArabicIsolatedForm: is_arabic()
                if info.is_arabic:
                    d_ai_feed(character, info)

        mean_mess_ratio = (
            d_sp.ratio
            + d_ta.ratio
            + d_up.ratio
            + d_sda.ratio
            + d_sr.ratio
            + d_sw.ratio
            + d_cu.ratio
            + d_sk.ratio
            + d_au.ratio
            + d_ai.ratio
        )

        if mean_mess_ratio >= maximum_threshold:
            break
    else:
        # Flush last word buffer in SuperWeirdWordPlugin via trailing newline.
        nl_info = ascii_info[10]  # "\n"
        d_sw_feed("\n", nl_info)
        if not is_pure_ascii:
            d_au_feed("\n", nl_info)
        d_up_feed("\n", nl_info)

        mean_mess_ratio = (
            d_sp.ratio
            + d_ta.ratio
            + d_up.ratio
            + d_sda.ratio
            + d_sr.ratio
            + d_sw.ratio
            + d_cu.ratio
            + d_sk.ratio
            + d_au.ratio
            + d_ai.ratio
        )

    if debug:  # Defensive:
        logger = getLogger("charset_normalizer")

        logger.log(
            TRACE,
            "Mess-detector extended-analysis start. "
            f"intermediary_mean_mess_ratio_calc={step} mean_mess_ratio={mean_mess_ratio} "
            f"maximum_threshold={maximum_threshold}",
        )

        if seq_len > 16:
            logger.log(TRACE, f"Starting with: {decoded_sequence[:16]}")
            logger.log(TRACE, f"Ending with: {decoded_sequence[-16::]}")

        for dt in [d_sp, d_ta, d_up, d_sda, d_sr, d_sw, d_cu, d_sk, d_au, d_ai]:
            logger.log(TRACE, f"{dt.__class__}: {dt.ratio}")

    return round(mean_mess_ratio, 3)
