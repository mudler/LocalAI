from __future__ import annotations

import re
import typing
from functools import partial
from urllib.parse import unquote as _unquote

from ..exceptions import LocationParseError
from .util import to_str

# We only want to normalize urls with an HTTP(S) scheme.
# urllib3 infers URLs without a scheme (None) to be http.
_NORMALIZABLE_SCHEMES = ("http", "https", None)

# Almost all of these patterns were derived from the
# 'rfc3986' module: https://github.com/python-hyper/rfc3986
_PERCENT_RE = re.compile(r"%[a-fA-F0-9]{2}")
_HOST_PERCENT_RE = re.compile(r"%[a-fA-F0-9]{2}|%")
_SCHEME_RE = re.compile(r"^(?:[a-zA-Z][a-zA-Z0-9+-]*:|/)")
_HOST_INVALID_CHAR_RE = re.compile(r"[\x00-\x20\x7f]")
_URI_RE = re.compile(
    r"^(?:([a-zA-Z][a-zA-Z0-9+.-]*):)?"
    r"(?://([^\\/?#]*))?"
    r"([^?#]*)"
    r"(?:\?([^#]*))?"
    r"(?:#(.*))?$",
    re.UNICODE | re.DOTALL,
)

_IPV4_PAT = r"(?:[0-9]{1,3}\.){3}[0-9]{1,3}"
_HEX_PAT = "[0-9A-Fa-f]{1,4}"
_LS32_PAT = "(?:{hex}:{hex}|{ipv4})".format(hex=_HEX_PAT, ipv4=_IPV4_PAT)
_subs = {"hex": _HEX_PAT, "ls32": _LS32_PAT}
_variations = [
    #                            6( h16 ":" ) ls32
    "(?:%(hex)s:){6}%(ls32)s",
    #                       "::" 5( h16 ":" ) ls32
    "::(?:%(hex)s:){5}%(ls32)s",
    # [               h16 ] "::" 4( h16 ":" ) ls32
    "(?:%(hex)s)?::(?:%(hex)s:){4}%(ls32)s",
    # [ *1( h16 ":" ) h16 ] "::" 3( h16 ":" ) ls32
    "(?:(?:%(hex)s:)?%(hex)s)?::(?:%(hex)s:){3}%(ls32)s",
    # [ *2( h16 ":" ) h16 ] "::" 2( h16 ":" ) ls32
    "(?:(?:%(hex)s:){0,2}%(hex)s)?::(?:%(hex)s:){2}%(ls32)s",
    # [ *3( h16 ":" ) h16 ] "::"    h16 ":"   ls32
    "(?:(?:%(hex)s:){0,3}%(hex)s)?::%(hex)s:%(ls32)s",
    # [ *4( h16 ":" ) h16 ] "::"              ls32
    "(?:(?:%(hex)s:){0,4}%(hex)s)?::%(ls32)s",
    # [ *5( h16 ":" ) h16 ] "::"              h16
    "(?:(?:%(hex)s:){0,5}%(hex)s)?::%(hex)s",
    # [ *6( h16 ":" ) h16 ] "::"
    "(?:(?:%(hex)s:){0,6}%(hex)s)?::",
]

_UNRESERVED_PAT = r"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._\-~"
_IPV6_PAT = "(?:" + "|".join([x % _subs for x in _variations]) + ")"
_ZONE_ID_PAT = "(?:%25|%)(?:[" + _UNRESERVED_PAT + "]|%[a-fA-F0-9]{2})+"
_IPV6_ADDRZ_PAT = r"\[" + _IPV6_PAT + r"(?:" + _ZONE_ID_PAT + r")?\]"
_REG_NAME_PAT = r"(?:[^\[\]%:/?#]|%[a-fA-F0-9]{2})*"
_TARGET_RE = re.compile(r"^(/[^?#]*)(?:\?([^#]*))?(?:#.*)?$")

_IPV4_RE = re.compile(
    r"^(?:0[xX][0-9a-fA-F]+|[0-9]+)(?:\.(?:0[xX][0-9a-fA-F]+|[0-9]+)){0,3}$"
)
_IPV6_RE = re.compile("^" + _IPV6_PAT + "$")
_IPV6_ADDRZ_RE = re.compile("^" + _IPV6_ADDRZ_PAT + "$")
_BRACELESS_IPV6_ADDRZ_RE = re.compile("^" + _IPV6_ADDRZ_PAT[2:-2] + "$")
_ZONE_ID_RE = re.compile("(" + _ZONE_ID_PAT + r")\]$")

_HOST_PORT_PAT = ("^(%s|%s|%s)(?::0*?(|0|[1-9][0-9]{0,4}))?$") % (
    _REG_NAME_PAT,
    _IPV4_PAT,
    _IPV6_ADDRZ_PAT,
)
_HOST_PORT_RE = re.compile(_HOST_PORT_PAT, re.UNICODE | re.DOTALL)

_UNRESERVED_CHARS = set(
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-~"
)
_SUB_DELIM_CHARS = set("!$&'()*+,;=")
_USERINFO_CHARS = _UNRESERVED_CHARS | _SUB_DELIM_CHARS | {":"}
_PATH_CHARS = _USERINFO_CHARS | {"@", "/"}
_QUERY_CHARS = _FRAGMENT_CHARS = _PATH_CHARS | {"?"}


class Url(
    typing.NamedTuple(
        "Url",
        [
            ("scheme", typing.Optional[str]),
            ("auth", typing.Optional[str]),
            ("host", typing.Optional[str]),
            ("port", typing.Optional[int]),
            ("path", typing.Optional[str]),
            ("query", typing.Optional[str]),
            ("fragment", typing.Optional[str]),
        ],
    )
):
    """
    Data structure for representing an HTTP URL. Used as a return value for
    :func:`parse_url`. Both the scheme and host are normalized as they are
    both case-insensitive according to RFC 3986.

    :param auth: User information as defined in RFC 3986 3.2.1. This
        component is kept percent-encoded. Use :attr:`auth_decoded` or
        :attr:`auth_decoded_joined` to get the decoded form.
    """

    def __new__(  # type: ignore[no-untyped-def]
        cls,
        scheme: str | None = None,
        auth: str | None = None,
        host: str | None = None,
        port: int | None = None,
        path: str | None = None,
        query: str | None = None,
        fragment: str | None = None,
    ):
        if path and not path.startswith("/"):
            path = "/" + path
        if scheme is not None:
            scheme = scheme.lower()
        return super().__new__(cls, scheme, auth, host, port, path, query, fragment)

    @property
    def auth_decoded(self) -> tuple[None, None] | tuple[str, str | None]:
        """
        User information with %-escapes decoded as UTF-8, returned as a
        ``(username, password)`` tuple.

        Both values are ``None`` if ``auth`` is ``None``.
        ``password`` is ``None`` if not present in the auth component.
        """
        if self.auth is None:
            return None, None
        username, sep, password = self.auth.partition(":")
        return (
            _unquote(username, encoding="utf-8"),
            _unquote(password, encoding="utf-8") if sep else None,
        )

    @property
    def auth_decoded_joined(self) -> str | None:
        """
        User information with %-escapes decoded as UTF-8, as a string
        prepared for encoding into an 'authorization: basic ...' header.

        This property does not choose the encoding used for an
        'authorization: basic ...' header. Use the ``basic_auth_encoding`` or
        ``proxy_basic_auth_encoding`` parameters of
        :func:`urllib3.util.make_headers` when converting the returned string
        into Basic authentication bytes.

        This is a convenience property that joins the username and
        password with a colon, if both are present.
        If only the username is present, a trailing colon is still
        appended.
        If ``auth`` is ``None``, this returns ``None``.
        """
        username, password = self.auth_decoded
        if username is None:
            return None
        return f"{username}:{password or ''}"

    @property
    def hostname(self) -> str | None:
        """For backwards-compatibility with urlparse. We're nice like that."""
        return self.host

    @property
    def request_uri(self) -> str:
        """Absolute path including the query string."""
        uri = self.path or "/"

        if self.query is not None:
            uri += "?" + self.query

        return uri

    @property
    def authority(self) -> str | None:
        """
        Authority component as defined in RFC 3986 3.2.
        This includes userinfo (auth), host and port.

        i.e.
            userinfo@host:port
        """
        userinfo = self.auth
        netloc = self.netloc
        if netloc is None or userinfo is None:
            return netloc
        else:
            return f"{userinfo}@{netloc}"

    @property
    def netloc(self) -> str | None:
        """
        Network location including host and port.

        If you need the equivalent of urllib.parse's ``netloc``,
        use the ``authority`` property instead.
        """
        if self.host is None:
            return None
        if self.port is not None:
            return f"{self.host}:{self.port}"
        return self.host

    @property
    def url(self) -> str:
        """
        Convert self into a url

        This function should more or less round-trip with :func:`.parse_url`. The
        returned url may not be exactly the same as the url inputted to
        :func:`.parse_url`, but it should be equivalent by the RFC (e.g., urls
        with a blank port will have : removed).

        Example:

        .. code-block:: python

            import urllib3

            U = urllib3.util.parse_url("https://google.com/mail/")

            print(U.url)
            # "https://google.com/mail/"

            print( urllib3.util.Url("https", "username:password",
                                    "host.com", 80, "/path", "query", "fragment"
                                    ).url
                )
            # "https://username:password@host.com:80/path?query#fragment"
        """
        scheme, auth, host, port, path, query, fragment = self
        url = ""

        # We use "is not None" we want things to happen with empty strings (or 0 port)
        if scheme is not None:
            url += scheme + "://"
        if auth is not None:
            url += auth + "@"
        if host is not None:
            url += host
        if port is not None:
            url += ":" + str(port)
        if path is not None:
            url += path
        if query is not None:
            url += "?" + query
        if fragment is not None:
            url += "#" + fragment

        return url

    def __str__(self) -> str:
        return self.url


@typing.overload
def _encode_invalid_chars(
    component: str, allowed_chars: typing.Container[str]
) -> str:  # Abstract
    ...


@typing.overload
def _encode_invalid_chars(
    component: None, allowed_chars: typing.Container[str]
) -> None:  # Abstract
    ...


def _encode_invalid_chars(
    component: str | None, allowed_chars: typing.Container[str]
) -> str | None:
    """Percent-encodes a URI component without reapplying
    onto an already percent-encoded component.
    """
    if component is None:
        return component

    component = to_str(component)

    # Fast path: an ASCII component that consists only of allowed characters
    # and contains no '%' needs neither encoding nor percent-normalization.
    # Components containing '%' always take the general path below because
    # how '%' is treated depends on whether the whole component is already
    # percent-encoded.
    if (
        "%" not in component
        and component.isascii()
        and all(map(allowed_chars.__contains__, component))
    ):
        return component

    # Normalize existing percent-encoded bytes.
    # Try to see if the component we're encoding is already percent-encoded
    # so we can skip all '%' characters but still encode all others.
    component, percent_encodings = _PERCENT_RE.subn(
        lambda match: match.group(0).upper(), component
    )

    uri_bytes = component.encode("utf-8", "surrogatepass")
    is_percent_encoded = percent_encodings == uri_bytes.count(b"%")
    encoded_component = bytearray()

    # Iterating over bytes yields integers; 0x25 is '%'.
    for byte_ord in uri_bytes:
        if (is_percent_encoded and byte_ord == 0x25) or (
            byte_ord < 128 and chr(byte_ord) in allowed_chars
        ):
            encoded_component.append(byte_ord)
        else:
            encoded_component += b"%%%02X" % byte_ord

    return encoded_component.decode()


def _remove_path_dot_segments(path: str) -> str:
    # See http://tools.ietf.org/html/rfc3986#section-5.2.4 for pseudo-code
    segments = path.split("/")  # Turn the path into a list of segments
    output = []  # Initialize the variable to use to store output

    for segment in segments:
        # '.' is the current directory, so ignore it, it is superfluous
        if segment == ".":
            continue
        # Anything other than '..', should be appended to the output
        if segment != "..":
            output.append(segment)
        # In this case segment == '..', if we can, we should pop the last
        # element
        elif output:
            output.pop()

    # If the path starts with '/' and the output is empty or the first string
    # is non-empty
    if path.startswith("/") and (not output or output[0]):
        output.insert(0, "")

    # If the path starts with '/.' or '/..' ensure we add one more empty
    # string to add a trailing '/'
    if path.endswith(("/.", "/..")):
        output.append("")

    return "/".join(output)


@typing.overload
def _normalize_host(host: None, scheme: str | None) -> None: ...


@typing.overload
def _normalize_host(host: str, scheme: str | None) -> str: ...


def _normalize_host(host: str | None, scheme: str | None) -> str | None:
    if host:
        invalid_host_char = _HOST_INVALID_CHAR_RE.search(host)
        if invalid_host_char:
            raise LocationParseError(
                f"Host {host!r} contains invalid character "
                f"{invalid_host_char.group()!r}"
            )
        if scheme in _NORMALIZABLE_SCHEMES:
            is_ipv6 = _IPV6_ADDRZ_RE.match(host)
            if is_ipv6:
                # IPv6 hosts of the form 'a::b%zone' are encoded in a URL as
                # such per RFC 6874: 'a::b%25zone'. Unquote the ZoneID
                # separator as necessary to return a valid RFC 4007 scoped IP.
                match = _ZONE_ID_RE.search(host)
                if match:
                    start, end = match.span(1)
                    zone_id = host[start:end]

                    if zone_id.startswith("%25") and zone_id != "%25":
                        zone_id = zone_id[3:]
                    else:
                        zone_id = zone_id[1:]
                    zone_id = _PERCENT_RE.sub(
                        partial(_normalize_zone_id_percent_encoding, error_host=host),
                        zone_id,
                    )
                    zone_id = _encode_invalid_chars(zone_id, _UNRESERVED_CHARS)
                    return f"{host[:start].lower()}%{zone_id}{host[end:]}"
                else:
                    return host.lower()
            elif not _IPV4_RE.match(host):
                if "%" in host:
                    host = _HOST_PERCENT_RE.sub(_normalize_host_percent_encoding, host)
                return to_str(
                    b".".join([_idna_encode(label) for label in host.split(".")]),
                    "ascii",
                )
    return host


def _decode_percent_encoding(match: re.Match[str], *, error_host: str) -> str:
    percent_encoded_octet = match.group(0)
    decoded_octet = chr(int(percent_encoded_octet[1:], 16))
    # Keep this narrower than _HOST_INVALID_CHAR_RE because "%20" is valid
    # percent-encoding and remains encoded.
    if decoded_octet < "\x20" or decoded_octet == "\x7f":
        raise LocationParseError(
            f"Host {error_host!r} contains invalid percent-encoded "
            f"control character {percent_encoded_octet!r}"
        )
    return decoded_octet


def _normalize_host_percent_encoding(match: re.Match[str]) -> str:
    # Reject invalid percent encodings
    if match.group(0) == "%":
        raise LocationParseError(f"{match.string!r} is not a valid host")
    decoded_octet = _decode_percent_encoding(match, error_host=match.string)
    if decoded_octet in _UNRESERVED_CHARS:
        return decoded_octet
    return match.group(0).upper()


def _normalize_zone_id_percent_encoding(
    match: re.Match[str], *, error_host: str
) -> str:
    _decode_percent_encoding(match, error_host=error_host)
    return match.group(0).upper()


def _idna_encode(name: str) -> bytes:
    if not name.isascii():
        try:
            import idna
        except ImportError:
            raise LocationParseError(
                "Unable to parse URL without the 'idna' module"
            ) from None

        try:
            return idna.encode(name.lower(), strict=True, std3_rules=True)
        except idna.IDNAError:
            raise LocationParseError(
                f"Name '{name}' is not a valid IDNA label"
            ) from None

    return _PERCENT_RE.sub(lambda match: match.group(0).upper(), name.lower()).encode(
        "ascii"
    )


def _encode_target(target: str) -> str:
    """Percent-encodes a request target so that there are no invalid characters

    Pre-condition for this function is that 'target' must start with '/'.
    If that is the case then _TARGET_RE will always produce a match.
    """
    match = _TARGET_RE.match(target)
    if not match:  # Defensive:
        raise LocationParseError(f"{target!r} is not a valid request URI")

    path, query = match.groups()
    encoded_target = _encode_invalid_chars(path, _PATH_CHARS)
    if query is not None:
        query = _encode_invalid_chars(query, _QUERY_CHARS)
        encoded_target += "?" + query
    return encoded_target


def parse_url(url: str) -> Url:
    """
    Given a url, return a parsed :class:`.Url` namedtuple. Best-effort is
    performed to parse incomplete urls. Fields not provided will be None.
    This parser is RFC 3986 and RFC 6874 compliant.

    The parser logic and helper functions are based heavily on
    work done in the ``rfc3986`` module.

    :param str url: URL to parse into a :class:`.Url` namedtuple.

    Partly backwards-compatible with :mod:`urllib.parse`.

    Example:

    .. code-block:: python

        import urllib3

        print( urllib3.util.parse_url('http://google.com/mail/'))
        # Url(scheme='http', host='google.com', port=None, path='/mail/', ...)

        print( urllib3.util.parse_url('google.com:80'))
        # Url(scheme=None, host='google.com', port=80, path=None, ...)

        print( urllib3.util.parse_url('/foo?bar'))
        # Url(scheme=None, host=None, port=None, path='/foo', query='bar', ...)
    """
    if not url:
        # Empty
        return Url()

    if not _SCHEME_RE.search(url):
        url = "//" + url

    scheme: str | None
    authority: str | None
    auth: str | None
    host: str | None
    port: str | None
    port_int: int | None
    path: str | None
    query: str | None
    fragment: str | None

    scheme, authority, path, query, fragment = _URI_RE.match(url).groups()  # type: ignore[union-attr]
    normalize_uri = scheme is None or scheme.lower() in _NORMALIZABLE_SCHEMES

    if scheme:
        scheme = scheme.lower()

    if authority:
        auth, _, host_port = authority.rpartition("@")
        auth = auth or None
        host_port_match = _HOST_PORT_RE.fullmatch(host_port)
        if not host_port_match:
            invalid_host_char = _HOST_INVALID_CHAR_RE.search(host_port)
            if invalid_host_char:
                raise LocationParseError(
                    f"Host {host_port!r} contains invalid character "
                    f"{invalid_host_char.group()!r}"
                )
            raise LocationParseError(f"{host_port!r} is not a valid host or port")
        host, port = host_port_match.groups()
        if auth and normalize_uri:
            auth = _encode_invalid_chars(auth, _USERINFO_CHARS)
        if port == "":
            port = None
    else:
        auth, host, port = None, None, None

    if port is not None:
        port_int = int(port)
        if not (0 <= port_int <= 65535):
            raise LocationParseError(url)
    else:
        port_int = None

    host = _normalize_host(host, scheme)

    if normalize_uri and path:
        path = _remove_path_dot_segments(path)
        path = _encode_invalid_chars(path, _PATH_CHARS)
    if normalize_uri and query:
        query = _encode_invalid_chars(query, _QUERY_CHARS)
    if normalize_uri and fragment:
        fragment = _encode_invalid_chars(fragment, _FRAGMENT_CHARS)

    # For the sake of backwards compatibility we put empty
    # string values for path if there are any defined values
    # beyond the path in the URL.
    # TODO: Remove this when we break backwards compatibility.
    if not path:
        if query is not None or fragment is not None:
            path = ""
        else:
            path = None

    return Url(
        scheme=scheme,
        auth=auth,
        host=host,
        port=port_int,
        path=path,
        query=query,
        fragment=fragment,
    )
