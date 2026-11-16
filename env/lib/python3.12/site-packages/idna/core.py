from __future__ import annotations

import bisect
import re
import unicodedata
import warnings
from typing import Literal

from . import idnadata
from .intranges import intranges_contain

_virama_combining_class = 9
_alabel_prefix = b"xn--"
_max_input_length = 1024
_max_domain_length = 253  # RFC 1035 octets, excluding any trailing dot
_STATUS_VALID, _STATUS_MAPPED, _STATUS_DEVIATION, _STATUS_IGNORED = b"VMDI"
_unicode_dots_re = re.compile("[\u002e\u3002\uff0e\uff61]")
_std3_disallowed_re = re.compile("[\x00-\x2c\x2f\x3a-\x40A-Z\x5b-\x60\x7b-\x7f]")
_bidi_rtl_first = frozenset({"R", "AL"})
_bidi_rtl_categories = frozenset({"R", "AL", "AN"})
_bidi_rtl_allowed = frozenset({"R", "AL", "AN", "EN", "ES", "CS", "ET", "ON", "BN", "NSM"})
_bidi_rtl_valid_ending = frozenset({"R", "AL", "EN", "AN"})
_bidi_rtl_numeric = frozenset({"AN", "EN"})
_bidi_ltr_allowed = frozenset({"L", "EN", "ES", "CS", "ET", "ON", "BN", "NSM"})
_bidi_ltr_valid_ending = frozenset({"L", "EN"})
_bidi_joiner_l_or_d = frozenset({"L", "D"})
_bidi_joiner_r_or_d = frozenset({"R", "D"})


def _joining_type(cp: int) -> str | None:
    for jt, ranges in idnadata.joining_types.items():
        if intranges_contain(cp, ranges):
            return jt
    return None


# Machine-readable identifiers for the rule an :class:`IDNAError` reports.
# These strings are stable and documented; exception message wording is not.
_ErrorCode = Literal[
    "input_too_long",
    "label_too_long",
    "domain_too_long",
    "empty_label",
    "empty_domain",
    "not_nfc",
    "hyphen_3_4",
    "hyphen_start_end",
    "leading_combiner",
    "disallowed_codepoint",
    "contextj",
    "contexto",
    "unknown_codepoint",
    "bidi_rule_1",
    "bidi_rule_2",
    "bidi_rule_3",
    "bidi_rule_4",
    "bidi_rule_5",
    "bidi_rule_6",
    "bidi_unknown_direction",
    "invalid_alabel",
    "non_canonical_alabel",
    "invalid_ascii",
    "invalid_utf8",
    "uts46_disallowed",
    "uts46_std3",
    "unsupported_errors",
]


class IDNAError(UnicodeError):
    """Base exception for all IDNA-encoding related problems.

    ``str(err)`` is a human-readable description of the failure. The
    exception also carries machine-readable attributes so callers do not
    need to parse the message:

    * ``code`` -- a short, stable identifier for the rule that failed, such
      as ``"disallowed_codepoint"`` or ``"bidi_rule_2"``; the full list is
      documented in the README. Message wording, by contrast, may change
      between releases.
    * ``text`` -- the label (or, for UTS #46 processing, the domain) that
      was being validated;
    * ``codepoint`` -- the offending codepoint, as an ``int``;
    * ``position`` -- the 1-based index of the offending character within
      ``text``, matching the position quoted in the message.

    Each is ``None`` when it does not apply.
    """

    code: str | None
    text: str | None
    codepoint: int | None
    position: int | None

    def __init__(
        self,
        *args: object,
        code: _ErrorCode | None = None,
        text: str | None = None,
        codepoint: int | None = None,
        position: int | None = None,
    ) -> None:
        super().__init__(*args)
        self.code = code
        self.text = text
        self.codepoint = codepoint
        self.position = position


class IDNABidiError(IDNAError):
    """Exception when bidirectional requirements are not satisfied"""


class InvalidCodepoint(IDNAError):
    """Exception when a disallowed or unallocated codepoint is used"""


class InvalidCodepointContext(IDNAError):
    """Exception when the codepoint is not valid in the context it is used"""


def _combining_class(cp: int) -> int:
    v = unicodedata.combining(chr(cp))
    if v == 0 and not unicodedata.name(chr(cp)):
        raise ValueError("Unknown character in unicodedata")
    return v


def _is_script(cp: str, script: str) -> bool:
    return intranges_contain(ord(cp), idnadata.scripts[script])


def _punycode(s: str) -> bytes:
    return s.encode("punycode")


def _unot(s: int) -> str:
    return f"U+{s:04X}"


def valid_label_length(label: bytes | str) -> bool:
    """Check that a label does not exceed the maximum permitted length.

    Per :rfc:`1035` (and :rfc:`5891` §4.2.4) a DNS label must not exceed
    63 octets. The argument may be either a :class:`str` (a U-label, where
    length is measured in characters) or :class:`bytes` (an A-label, where
    length is measured in octets).

    :param label: The label to check.
    :returns: ``True`` if the label is within the length limit, otherwise
        ``False``.
    """
    return len(label) <= 63


def valid_string_length(domain: bytes | str, trailing_dot: bool) -> bool:
    """Check that a full domain name does not exceed the maximum length.

    Per :rfc:`1035`, a domain name is limited to 253 octets when no trailing
    dot is present, or 254 octets when one is included.

    :param domain: The full (possibly multi-label) domain name.
    :param trailing_dot: ``True`` if ``domain`` includes a trailing ``.``.
    :returns: ``True`` if the domain is within the length limit, otherwise
        ``False``.
    """
    return len(domain) <= _max_domain_length + trailing_dot


def check_bidi(label: str, check_ltr: bool = False) -> bool:
    """Validate the Bidi Rule from :rfc:`5893` for a single label.

    The Bidi Rule constrains how bidirectional characters (Hebrew, Arabic,
    etc.) may appear within a label. By default the check is only applied
    when the label contains at least one right-to-left character (Unicode
    bidirectional categories ``R``, ``AL``, or ``AN``); set ``check_ltr``
    to ``True`` to apply it to LTR-only labels as well.

    :param label: The label to validate, as a Unicode string.
    :param check_ltr: If ``True``, apply the rules even when the label
        contains no RTL characters.
    :returns: ``True`` if the label satisfies the Bidi Rule.
    :raises IDNABidiError: If any of Bidi Rule conditions 1-6 are violated,
        or if the directional category of a codepoint cannot be determined.
    """
    if len(label) > _max_input_length:
        raise IDNAError("Label too long", code="input_too_long")
    # Bidi rules should only be applied if string contains RTL characters
    bidi_label = False
    for idx, cp in enumerate(label, 1):
        direction = unicodedata.bidirectional(cp)
        if direction == "":
            # String likely comes from a newer version of Unicode
            raise IDNABidiError(
                f"Unknown directionality in label {label!r} at position {idx}",
                code="bidi_unknown_direction",
                text=label,
                codepoint=ord(cp),
                position=idx,
            )
        if direction in _bidi_rtl_categories:
            bidi_label = True
    if not bidi_label and not check_ltr:
        return True

    # Bidi rule 1
    direction = unicodedata.bidirectional(label[0])
    if direction in _bidi_rtl_first:
        rtl = True
    elif direction == "L":
        rtl = False
    else:
        raise IDNABidiError(
            f"First codepoint in label {label!r} must be directionality L, R or AL",
            code="bidi_rule_1",
            text=label,
            codepoint=ord(label[0]),
            position=1,
        )

    valid_ending = False
    ending_idx = 1
    number_type: str | None = None
    for idx, cp in enumerate(label, 1):
        direction = unicodedata.bidirectional(cp)

        if rtl:
            # Bidi rule 2
            if direction not in _bidi_rtl_allowed:
                raise IDNABidiError(
                    f"Invalid direction for codepoint at position {idx} in a right-to-left label",
                    code="bidi_rule_2",
                    text=label,
                    codepoint=ord(cp),
                    position=idx,
                )
            # Bidi rule 3
            if direction in _bidi_rtl_valid_ending:
                valid_ending = True
                ending_idx = idx
            elif direction != "NSM":
                valid_ending = False
                ending_idx = idx
            # Bidi rule 4
            if direction in _bidi_rtl_numeric:
                if not number_type:
                    number_type = direction
                elif number_type != direction:
                    raise IDNABidiError(
                        "Can not mix numeral types in a right-to-left label",
                        code="bidi_rule_4",
                        text=label,
                        codepoint=ord(cp),
                        position=idx,
                    )
        else:
            # Bidi rule 5
            if direction not in _bidi_ltr_allowed:
                raise IDNABidiError(
                    f"Invalid direction for codepoint at position {idx} in a left-to-right label",
                    code="bidi_rule_5",
                    text=label,
                    codepoint=ord(cp),
                    position=idx,
                )
            # Bidi rule 6
            if direction in _bidi_ltr_valid_ending:
                valid_ending = True
                ending_idx = idx
            elif direction != "NSM":
                valid_ending = False
                ending_idx = idx

    if not valid_ending:
        # Rules 3 and 6 concern the last character that is not a
        # non-spacing mark, which is what ``ending_idx`` tracks.
        raise IDNABidiError(
            "Label ends with illegal codepoint directionality",
            code="bidi_rule_3" if rtl else "bidi_rule_6",
            text=label,
            codepoint=ord(label[ending_idx - 1]),
            position=ending_idx,
        )

    return True


def check_initial_combiner(label: str) -> bool:
    """Reject labels that begin with a combining mark.

    Per :rfc:`5891` §4.2.3.2 a label must not start with a character of
    Unicode general category ``M`` (Mark).

    :param label: The label to check.
    :returns: ``True`` if the first character is not a combining mark.
    :raises IDNAError: If the label begins with a combining character.
    """
    if label and unicodedata.category(label[0])[0] == "M":
        raise IDNAError(
            "Label begins with an illegal combining character",
            code="leading_combiner",
            text=label,
            codepoint=ord(label[0]),
            position=1,
        )
    return True


def check_hyphen_ok(label: str) -> bool:
    """Validate the hyphen restrictions for a label.

    Per :rfc:`5891` §4.2.3.1 a label must not start or end with a hyphen
    (``U+002D``), and must not have hyphens in both the third and fourth
    positions (the prefix reserved for A-labels).

    :param label: The label to check.
    :returns: ``True`` if the hyphen restrictions are satisfied.
    :raises IDNAError: If any of the hyphen restrictions are violated.
    """
    if label[2:4] == "--":
        raise IDNAError("Label has disallowed hyphens in 3rd and 4th position", code="hyphen_3_4")
    if label.startswith("-") or label.endswith("-"):
        raise IDNAError("Label must not start or end with a hyphen", code="hyphen_start_end")
    return True


def check_nfc(label: str) -> None:
    """Require that a label is in Unicode Normalization Form C.

    :param label: The label to check.
    :raises IDNAError: If ``label`` differs from its NFC normalisation.
    """
    if len(label) > _max_input_length:
        raise IDNAError("Label too long", code="input_too_long")
    if unicodedata.normalize("NFC", label) != label:
        raise IDNAError("Label must be in Normalization Form C", code="not_nfc")


def valid_contextj(label: str, pos: int) -> bool:
    """Validate the CONTEXTJ rules from :rfc:`5892` Appendix A.

    These rules govern the contextual use of the joiner codepoints
    ``U+200C`` (ZERO WIDTH NON-JOINER, Appendix A.1) and ``U+200D``
    (ZERO WIDTH JOINER, Appendix A.2) within a label.

    :param label: The label containing the codepoint.
    :param pos: Index of the joiner codepoint within ``label``.
    :returns: ``True`` if the codepoint at ``pos`` satisfies its CONTEXTJ
        rule, ``False`` otherwise (including when the codepoint at
        ``pos`` is not a recognised joiner).
    :raises ValueError: If an adjacent codepoint has no Unicode name when
        determining its combining class.
    :raises IDNAError: If ``label`` exceeds the defensive input length limit.
    """
    if len(label) > _max_input_length:
        raise IDNAError("Label too long", code="input_too_long")
    cp_value = ord(label[pos])

    if cp_value == 0x200C:
        if pos > 0 and _combining_class(ord(label[pos - 1])) == _virama_combining_class:
            return True

        ok = False
        for i in range(pos - 1, -1, -1):
            joining_type = _joining_type(ord(label[i]))
            if joining_type == "T":
                continue
            if joining_type in _bidi_joiner_l_or_d:
                ok = True
                break
            break

        if not ok:
            return False

        ok = False
        for i in range(pos + 1, len(label)):
            joining_type = _joining_type(ord(label[i]))
            if joining_type == "T":
                continue
            if joining_type in _bidi_joiner_r_or_d:
                ok = True
                break
            break
        return ok

    if cp_value == 0x200D:
        return pos > 0 and _combining_class(ord(label[pos - 1])) == _virama_combining_class

    return False


def valid_contexto(label: str, pos: int, exception: bool = False) -> bool:
    """Validate the CONTEXTO rules from :rfc:`5892` Appendix A.

    Covers the contextual rules for codepoints such as MIDDLE DOT
    (``U+00B7``), Greek lower numeral sign, Hebrew punctuation, Katakana
    middle dot, and the Arabic-Indic / Extended Arabic-Indic digit ranges.

    :param label: The label containing the codepoint.
    :param pos: Index of the codepoint within ``label``.
    :param exception: Reserved for forward compatibility; currently unused.
    :returns: ``True`` if the codepoint at ``pos`` satisfies its CONTEXTO
        rule, ``False`` otherwise (including when the codepoint is not a
        recognised CONTEXTO codepoint).
    :raises IDNAError: If ``label`` exceeds the defensive input length limit.
    """
    if len(label) > _max_input_length:
        raise IDNAError("Label too long", code="input_too_long")
    cp_value = ord(label[pos])

    if cp_value == 0x00B7:
        return 0 < pos < len(label) - 1 and ord(label[pos - 1]) == 0x006C and ord(label[pos + 1]) == 0x006C

    if cp_value == 0x0375:
        if pos < len(label) - 1 and len(label) > 1:
            return _is_script(label[pos + 1], "Greek")
        return False

    if cp_value in {0x05F3, 0x05F4}:
        if pos > 0:
            return _is_script(label[pos - 1], "Hebrew")
        return False

    if cp_value == 0x30FB:
        for cp in label:
            if cp == "\u30fb":
                continue
            if _is_script(cp, "Hiragana") or _is_script(cp, "Katakana") or _is_script(cp, "Han"):
                return True
        return False

    if 0x660 <= cp_value <= 0x669:
        return not any(0x6F0 <= ord(cp) <= 0x06F9 for cp in label)

    if 0x6F0 <= cp_value <= 0x6F9:
        return not any(0x660 <= ord(cp) <= 0x0669 for cp in label)

    return False


def check_label(label: str | bytes | bytearray) -> None:
    """Run the full set of IDNA 2008 validity checks on a single label.

    Applies, in order: NFC normalisation (:func:`check_nfc`), hyphen
    restrictions (:func:`check_hyphen_ok`), the no-leading-combiner rule
    (:func:`check_initial_combiner`), per-codepoint validity (PVALID,
    CONTEXTJ, CONTEXTO classes from :rfc:`5892`), and the Bidi Rule
    (:func:`check_bidi`).

    :param label: The label to validate. ``bytes`` or ``bytearray`` input
        is decoded as UTF-8 first.
    :raises IDNAError: If the label is empty or fails a structural rule.
    :raises InvalidCodepoint: If the label contains a DISALLOWED or
        UNASSIGNED codepoint.
    :raises InvalidCodepointContext: If a CONTEXTJ or CONTEXTO codepoint
        is not valid in its context.
    :raises IDNABidiError: If the Bidi Rule is violated.
    """
    if len(label) > _max_input_length:
        raise IDNAError("Label too long", code="input_too_long")
    if isinstance(label, (bytes, bytearray)):
        try:
            label = label.decode("utf-8")
        except UnicodeDecodeError as err:
            raise IDNAError("Invalid UTF-8 in label", code="invalid_utf8") from err
    if len(label) == 0:
        raise IDNAError("Empty Label", code="empty_label")

    # Check against the domain length rather than the label length to
    # support some UTS #46 use cases, while still bounding the work done
    # by the label contextual rules below.
    if not valid_string_length(label, trailing_dot=True):
        raise IDNAError("Label too long", code="label_too_long")

    check_nfc(label)
    check_hyphen_ok(label)
    check_initial_combiner(label)

    for pos, cp in enumerate(label):
        cp_value = ord(cp)
        if intranges_contain(cp_value, idnadata.codepoint_classes["PVALID"]):
            continue
        if intranges_contain(cp_value, idnadata.codepoint_classes["CONTEXTJ"]):
            try:
                contextj_ok = valid_contextj(label, pos)
            except ValueError as err:
                raise IDNAError(
                    f"Unknown codepoint adjacent to joiner {_unot(cp_value)} at position {pos + 1} in {label!r}",
                    code="unknown_codepoint",
                    text=label,
                    codepoint=cp_value,
                    position=pos + 1,
                ) from err
            if not contextj_ok:
                raise InvalidCodepointContext(
                    f"Joiner {_unot(cp_value)} not allowed at position {pos + 1} in {label!r}",
                    code="contextj",
                    text=label,
                    codepoint=cp_value,
                    position=pos + 1,
                )
        elif intranges_contain(cp_value, idnadata.codepoint_classes["CONTEXTO"]):
            if not valid_contexto(label, pos):
                raise InvalidCodepointContext(
                    f"Codepoint {_unot(cp_value)} not allowed at position {pos + 1} in {label!r}",
                    code="contexto",
                    text=label,
                    codepoint=cp_value,
                    position=pos + 1,
                )
        else:
            raise InvalidCodepoint(
                f"Codepoint {_unot(cp_value)} at position {pos + 1} of {label!r} not allowed",
                code="disallowed_codepoint",
                text=label,
                codepoint=cp_value,
                position=pos + 1,
            )

    check_bidi(label)


def alabel(label: str) -> bytes:
    """Convert a single U-label into its A-label form.

    The result is the ASCII-Compatible Encoding (ACE) form per :rfc:`5891`
    §4: the label is validated, Punycode-encoded, and prefixed with
    ``xn--``. Pure ASCII labels that are already valid IDNA labels are
    returned unchanged (as :class:`bytes`).

    :param label: The label to convert, as a Unicode string.
    :returns: The A-label as ASCII-encoded :class:`bytes`.
    :raises IDNAError: If the label is invalid or the resulting A-label
        exceeds 63 octets.
    """
    if len(label) > _max_input_length:
        raise IDNAError("Label too long", code="input_too_long")
    try:
        label_bytes = label.encode("ascii")
    except UnicodeEncodeError:
        pass
    else:
        ulabel(label_bytes)
        if not valid_label_length(label_bytes):
            raise IDNAError("Label too long", code="label_too_long")
        return label_bytes

    check_label(label)
    label_bytes = _alabel_prefix + _punycode(label)

    if not valid_label_length(label_bytes):
        raise IDNAError("Label too long", code="label_too_long")

    return label_bytes


def ulabel(label: str | bytes | bytearray) -> str:
    """Convert a single A-label into its U-label form.

    Performs the inverse of :func:`alabel`: an ``xn--``-prefixed label is
    Punycode-decoded and validated, and is rejected unless it is the
    canonical A-label for the decoded U-label (:rfc:`5891` §5.3). Labels
    that are already Unicode (or plain ASCII without the ACE prefix) are
    validated and returned as a Unicode string.

    :param label: The label to convert. ``bytes`` or ``bytearray`` input
        is treated as ASCII.
    :returns: The U-label as a Unicode string.
    :raises IDNAError: If the label is malformed or fails validation.
    """
    if len(label) > _max_input_length:
        raise IDNAError("Label too long", code="input_too_long")
    if not isinstance(label, (bytes, bytearray)):
        try:
            label_bytes = label.encode("ascii")
        except UnicodeEncodeError:
            check_label(label)
            return label
    else:
        label_bytes = bytes(label)
        if not label_bytes.isascii():
            raise IDNAError("Invalid ASCII in A-label", code="invalid_ascii")

    label_bytes = label_bytes.lower()
    if label_bytes.startswith(_alabel_prefix):
        label_bytes = label_bytes[len(_alabel_prefix) :]
        if not label_bytes:
            raise IDNAError("Malformed A-label, no Punycode eligible content found", code="invalid_alabel")
        if label_bytes.endswith(b"-"):
            raise IDNAError("A-label must not end with a hyphen", code="invalid_alabel")
    else:
        check_label(label_bytes)
        return label_bytes.decode("ascii")

    try:
        label = label_bytes.decode("punycode")
    except UnicodeError as err:
        raise IDNAError("Invalid A-label", code="invalid_alabel") from err
    # RFC 5891 §5.3: the label is rejected unless re-encoding the decoded
    # form reproduces the (lowercased) input. This catches "fake A-labels"
    # (RFC 5890 §2.3.2.1) such as ``xn---bbk``, a non-canonical Punycode
    # spelling of ``xn--bbk`` that would otherwise decode to the same
    # U-label and so display identically to a different wire-format name.
    if _punycode(label) != label_bytes:
        raise IDNAError("A-label is not the canonical Punycode encoding of its U-label", code="non_canonical_alabel")
    check_label(label)
    return label


def _check_std3(text: str, domain: str, offset: int) -> None:
    """Raise if ``text``, a slice of ``domain`` starting at ``offset`` that
    UTS #46 mapping left unchanged, contains an ASCII character disallowed
    under ``UseSTD3ASCIIRules``."""
    match = _std3_disallowed_re.search(text)
    if match:
        codepoint = ord(match.group())
        position = offset + match.start() + 1
        raise InvalidCodepoint(
            f"Codepoint {_unot(codepoint)} not allowed at position {position} in {domain!r}",
            code="uts46_std3",
            text=domain,
            codepoint=codepoint,
            position=position,
        )


def _warn_transitional() -> None:
    warnings.warn(
        "Transitional processing is deprecated in UTS #46 and has no effect. "
        "The transitional argument will be removed in a future version.",
        DeprecationWarning,
        stacklevel=3,
    )


def uts46_remap(domain: str, std3_rules: bool = True, transitional: bool = False) -> str:
    """Apply the UTS #46 character mapping to a domain string.

    Implements the mapping table from `UTS #46 §4
    <https://www.unicode.org/reports/tr46/>`_: each character is kept,
    replaced, or rejected based on its status (``V``, ``M``, ``D``,
    ``I``, ``X``). The result is returned in Normalisation Form C.

    :param domain: The full domain name to remap.
    :param std3_rules: If ``True``, apply UTS #46's ``UseSTD3ASCIIRules``:
        after mapping, any ASCII character other than a lowercase letter,
        digit, hyphen or the label separator ``.`` is rejected, whether it
        appeared in the input or was produced by a mapping (e.g. U+FF01
        FULLWIDTH EXCLAMATION MARK maps to ``!``). If ``False``, such
        characters are passed through.
    :param transitional: Deprecated and ignored. UTS #46 deprecated
        transitional processing in Unicode 15.1 and deviation (status
        ``D``) codepoints are now always kept, so this has no effect
        beyond emitting a :class:`DeprecationWarning`. It will be removed
        in a future version.
    :returns: The remapped domain, in Normalisation Form C.
    :raises InvalidCodepoint: If the domain contains a disallowed
        codepoint under the chosen rules.
    :raises IDNAError: If ``domain`` exceeds the defensive input length limit.
    """
    if transitional:
        _warn_transitional()
    if len(domain) > _max_input_length:
        raise IDNAError("Domain too long", code="input_too_long")
    if domain.isascii():
        # The only ASCII mapping in UTS #46 is upper- to lowercase, and
        # ASCII is invariant under NFC, so lowercasing is the whole job.
        result = domain.lower()
        if std3_rules:
            _check_std3(result, domain, 0)
        return result

    from .uts46data import uts46_replacements, uts46_starts, uts46_statuses

    # ``start`` marks the run of unchanged input not yet copied; a run is
    # only sliced out when a character must be replaced or dropped, so the
    # common no-change case makes no copy. STD3 is checked per output piece
    # to report a violation at its input position.
    output: list[str] = []
    start = 0
    for pos, char in enumerate(domain):
        code_point = ord(char)
        i = code_point if code_point < 256 else bisect.bisect_right(uts46_starts, code_point) - 1
        status = uts46_statuses[i]
        # UTS #46 §4: V valid, D deviation (kept), M mapped, I ignored,
        # anything else disallowed.
        if status == _STATUS_VALID:
            continue
        if status == _STATUS_MAPPED:
            replacement = uts46_replacements[i]
        elif status == _STATUS_DEVIATION:
            continue
        elif status == _STATUS_IGNORED:
            replacement = None
        else:
            raise InvalidCodepoint(
                f"Codepoint {_unot(code_point)} not allowed at position {pos + 1} in {domain!r}",
                code="uts46_disallowed",
                text=domain,
                codepoint=code_point,
                position=pos + 1,
            )
        if start < pos:
            run = domain[start:pos]
            if std3_rules:
                _check_std3(run, domain, start)
            output.append(run)
        if replacement:
            if std3_rules and _std3_disallowed_re.search(replacement):
                raise InvalidCodepoint(
                    f"Codepoint {_unot(code_point)} not allowed at position {pos + 1} in {domain!r}",
                    code="uts46_std3",
                    text=domain,
                    codepoint=code_point,
                    position=pos + 1,
                )
            output.append(replacement)
        start = pos + 1

    if start == 0:
        if std3_rules:
            _check_std3(domain, domain, 0)
        return unicodedata.normalize("NFC", domain)
    tail = domain[start:]
    if std3_rules:
        _check_std3(tail, domain, start)
    output.append(tail)
    return unicodedata.normalize("NFC", "".join(output))


def encode(
    s: str | bytes | bytearray,
    strict: bool = False,
    uts46: bool = False,
    std3_rules: bool = False,
    transitional: bool = False,
) -> bytes:
    """Encode a Unicode domain name into its ASCII (A-label) form.

    Splits the input on label separators (only ``U+002E`` if ``strict`` is
    set; otherwise also IDEOGRAPHIC FULL STOP ``U+3002``, FULLWIDTH FULL
    STOP ``U+FF0E``, and HALFWIDTH IDEOGRAPHIC FULL STOP ``U+FF61``),
    encodes each label with :func:`alabel`, and rejoins them with ``.``.
    Optionally pre-processes the input through :func:`uts46_remap`.

    :param s: The domain name to encode.
    :param strict: If ``True``, only ``U+002E`` is recognised as a label
        separator.
    :param uts46: If ``True``, apply UTS #46 mapping before encoding.
    :param std3_rules: Forwarded to :func:`uts46_remap` when ``uts46`` is
        ``True``.
    :param transitional: Deprecated and ignored (see :func:`uts46_remap`):
        emits a :class:`DeprecationWarning` and will be removed in a
        future version.
    :returns: The encoded domain as ASCII :class:`bytes`.
    :raises IDNAError: If the domain is empty, contains an invalid label,
        or exceeds the maximum domain length.
    """
    if transitional:
        _warn_transitional()
    if not isinstance(s, str):
        try:
            s = str(s, "ascii")
        except (UnicodeDecodeError, TypeError) as err:
            raise IDNAError(
                "should pass a unicode string to the function rather than a byte string.", code="invalid_ascii"
            ) from err
    if len(s) > _max_input_length:
        raise IDNAError("Domain too long", code="input_too_long")
    if uts46:
        s = uts46_remap(s, std3_rules)

    if not valid_string_length(s, trailing_dot=True):
        raise IDNAError("Domain too long", code="domain_too_long")

    trailing_dot = False
    result = []
    labels = s.split(".") if strict else _unicode_dots_re.split(s)
    if not labels or labels == [""]:
        raise IDNAError("Empty domain", code="empty_domain")
    if labels[-1] == "":
        del labels[-1]
        trailing_dot = True
    for label in labels:
        s = alabel(label)
        if s:
            result.append(s)
        else:
            raise IDNAError("Empty label", code="empty_label")
    if trailing_dot:
        result.append(b"")
    s = b".".join(result)
    if not valid_string_length(s, trailing_dot):
        raise IDNAError("Domain too long", code="domain_too_long")
    return s


def decode(
    s: str | bytes | bytearray,
    strict: bool = False,
    uts46: bool = False,
    std3_rules: bool = False,
    display: bool = False,
) -> str:
    """Decode an A-label-encoded domain name back to Unicode.

    Splits the input on label separators (see :func:`encode` for the
    rules), decodes each label with :func:`ulabel`, and rejoins them
    with ``.``. Optionally pre-processes the input through
    :func:`uts46_remap`.

    :param s: The domain name to decode.
    :param strict: If ``True``, only ``U+002E`` is recognised as a label
        separator.
    :param uts46: If ``True``, apply UTS #46 mapping before decoding.
    :param std3_rules: Forwarded to :func:`uts46_remap` when ``uts46`` is
        ``True``.
    :param display: If ``True``, any ``xn--`` label that fails IDNA
        validation is passed through unchanged (lowercased) rather than
        aborting the whole call. Intended for "decode for display"
        consumers (e.g. URL libraries, HTTP clients) that want to show
        the user the label as it appears on the wire when it cannot be
        rendered as Unicode. Matches the per-label recovery prescribed
        by UTS #46 §4 and the WHATWG URL "domain to Unicode" algorithm.
    :returns: The decoded domain as a Unicode string.
    :raises IDNAError: If the input is not valid ASCII, contains an
        invalid label, or is empty.
    """
    if not isinstance(s, str):
        try:
            s = str(s, "ascii")
        except (UnicodeDecodeError, TypeError) as err:
            raise IDNAError("Invalid ASCII in A-label", code="invalid_ascii") from err
    if len(s) > _max_input_length:
        raise IDNAError("Domain too long", code="input_too_long")
    if uts46:
        s = uts46_remap(s, std3_rules, False)
    if not valid_string_length(s, trailing_dot=True):
        raise IDNAError("Domain too long", code="domain_too_long")
    trailing_dot = False
    result = []
    labels = s.split(".") if strict else _unicode_dots_re.split(s)
    if not labels or labels == [""]:
        raise IDNAError("Empty domain", code="empty_domain")
    if not labels[-1]:
        del labels[-1]
        trailing_dot = True
    for label in labels:
        try:
            u = ulabel(label)
        except IDNAError:
            if display and label[:4].lower() == "xn--":
                u = label.lower()
            else:
                raise
        if u:
            result.append(u)
        else:
            raise IDNAError("Empty label", code="empty_label")
    if trailing_dot:
        result.append("")
    return ".".join(result)
