// SPDX-License-Identifier: MIT

// Package account is the connection a person gave the Mail add-on: where the
// IMAP and SMTP servers are, how to reach them safely and who logs in. The host
// keeps the whole record sealed as the credential of the binding and hands it
// over with each call, so the add-on stores none of it. The record is JSON with
// a version, and a field the add-on does not know is refused.
package account

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Version is the one version of the record.
const Version = 1

// Security says how a connection is protected.
type Security string

const (
	// TLS is implicit TLS from the first byte (IMAP 993, SMTP 465).
	TLS Security = "tls"
	// StartTLS upgrades a plain connection before any login (IMAP 143, SMTP 587).
	StartTLS Security = "starttls"
	// None sends everything, the password included, in clear text. The person
	// has to choose it, and the page warns about it.
	None Security = "none"
)

// Limits of the record.
const (
	// MaxBytes is the largest credential the host passes (contract.MaxCredentialBytes).
	MaxBytes       = 4096
	MaxFolders     = 20
	MaxFolderBytes = 200
	MaxNameRunes   = 100
	MaxPassword    = 512
	MaxUsername    = 256
)

// IMAPPorts and SMTPPorts are the ports a connection may use; package dial enforces them. They are the
// well-known ports of each protocol. Port 25 is left out on purpose: it is
// the port of server-to-server mail, which the network policy of the host
// refuses for commands too.
var (
	IMAPPorts = []int{143, 993}
	SMTPPorts = []int{465, 587}
)

// Endpoint is one server.
type Endpoint struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Security Security `json:"security"`
}

// Account is one mailbox with its two servers.
type Account struct {
	Version int `json:"v"`
	// Address is the mailbox address. It is the From address of the mail the
	// add-on sends, and the key the host holds the connection under.
	Address string `json:"address"`
	// Name is the display name of the sender, optional.
	Name string `json:"name,omitempty"`
	// Username is the login of both servers; Password its password.
	Username string   `json:"username"`
	Password string   `json:"password"`
	IMAP     Endpoint `json:"imap"`
	SMTP     Endpoint `json:"smtp"`
	// Folders is the optional allowlist of folders the tools may reach.
	// Empty: every folder.
	Folders []string `json:"folders,omitempty"`
	// DraftsFolder is the folder of save-draft when the server names none
	// with the SPECIAL-USE attribute \Drafts.
	DraftsFolder string `json:"draftsFolder,omitempty"`
}

// Errors of Parse. Their text never holds the record.
var (
	ErrMalformed = errors.New("the saved connection is not readable; save it again on the Mail page")
	ErrInvalid   = errors.New("the saved connection is not valid")
)

// Parse reads and checks the credential of a call.
func Parse(token string) (Account, error) {
	if token == "" || len(token) > MaxBytes {
		return Account{}, ErrMalformed
	}
	var a Account
	dec := json.NewDecoder(bytes.NewReader([]byte(token)))
	dec.DisallowUnknownFields()
	if dec.Decode(&a) != nil {
		return Account{}, ErrMalformed
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Account{}, ErrMalformed
	}
	if err := a.Validate(); err != nil {
		return Account{}, err
	}
	return a, nil
}

// Encode is the record as the page seals it. It validates first.
func (a Account) Encode() (string, error) {
	if a.Version == 0 {
		a.Version = Version
	}
	if err := a.Validate(); err != nil {
		return "", err
	}
	// #nosec G117 -- the record is the credential: the host seals this string and never logs it
	b, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	if len(b) > MaxBytes {
		return "", fmt.Errorf("%w: it is longer than %d bytes", ErrInvalid, MaxBytes)
	}
	return string(b), nil
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Validate checks every field. The messages name the field and never its value.
func (a Account) Validate() error {
	if a.Version != Version {
		return ErrMalformed
	}
	if err := ValidAddress(a.Address); err != nil {
		return invalid("address")
	}
	if utf8.RuneCountInString(a.Name) > MaxNameRunes || hasControl(a.Name) {
		return invalid("name")
	}
	if a.Username == "" || len(a.Username) > MaxUsername || hasControl(a.Username) {
		return invalid("username")
	}
	if a.Password == "" || len(a.Password) > MaxPassword || hasControl(a.Password) {
		return invalid("password")
	}
	if err := a.IMAP.validate("imap"); err != nil {
		return err
	}
	if err := a.SMTP.validate("smtp"); err != nil {
		return err
	}
	if len(a.Folders) > MaxFolders {
		return invalid("folders: at most %d", MaxFolders)
	}
	for _, f := range a.Folders {
		if !validFolder(f) {
			return invalid("folders")
		}
	}
	if a.DraftsFolder != "" && !validFolder(a.DraftsFolder) {
		return invalid("draftsFolder")
	}
	return nil
}

func (e Endpoint) validate(what string) error {
	if err := ValidHost(e.Host); err != nil {
		return invalid("%s host", what)
	}
	// Which ports are allowed is the dialer's rule (package dial), so a test
	// server on any port can be used; here the port only has to be a port.
	if e.Port < 1 || e.Port > 65535 {
		return invalid("%s port", what)
	}
	switch e.Security {
	case TLS, StartTLS, None:
	default:
		return invalid("%s security", what)
	}
	return nil
}

// ValidHost accepts a host name or an IP literal, without port, scheme, path or
// space. Whether the address is reachable is the dialer's rule, not this one's.
func ValidHost(h string) error {
	if h == "" || len(h) > 253 || strings.TrimSpace(h) != h {
		return errors.New("host")
	}
	for _, r := range h {
		if r <= 0x20 || r >= 0x7f || strings.ContainsRune("/@\\?#:[]%", r) {
			if r == ':' && net.ParseIP(h) != nil {
				continue
			}
			return errors.New("host")
		}
	}
	return nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return !utf8.ValidString(s)
}

func validFolder(f string) bool {
	return f != "" && len(f) <= MaxFolderBytes && !hasControl(f)
}

// ValidAddress accepts one plain address, user@host, with no display name and
// no control character. The check is that of net/mail, plus the shape.
func ValidAddress(s string) error {
	if s == "" || len(s) > 254 || hasControl(s) || strings.ContainsAny(s, "<>,;\"\\ ") {
		return errors.New("address")
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || a.Name != "" {
		return errors.New("address")
	}
	at := strings.LastIndexByte(s, '@')
	if at < 1 || at == len(s)-1 || !strings.Contains(s[at+1:], ".") && !isLiteral(s[at+1:]) {
		return errors.New("address")
	}
	return nil
}

func isLiteral(h string) bool { return strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") }

// AllowsFolder tells whether the tools may reach the folder. With no
// allowlist every folder is allowed. Names compare exactly, except INBOX,
// which IMAP matches without regard to case.
func (a Account) AllowsFolder(name string) bool {
	if len(a.Folders) == 0 {
		return true
	}
	for _, f := range a.Folders {
		if f == name || (strings.EqualFold(f, "INBOX") && strings.EqualFold(name, "INBOX")) {
			return true
		}
	}
	return false
}
