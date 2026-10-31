// SPDX-License-Identifier: MIT
package egress

import (
	"encoding/binary"
	"errors"
	"io"
)

// errNotHello says the first bytes of a tunnel are not a TLS ClientHello.
var errNotHello = errors.New("not a TLS ClientHello")

// maxRecord is the largest TLS record payload (2^14) the proxy reads.
const maxRecord = 16 << 10

// readClientHello reads the first TLS record of a tunnel and returns its
// bytes, to send on unchanged, and the server name of its ClientHello. The
// proxy only reads it: nothing is decrypted or changed. A hello longer than
// one record is read from its first record, which holds the server name in
// every common client.
func readClientHello(r io.Reader) ([]byte, string, error) {
	head := make([]byte, 5)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, "", err
	}
	if head[0] != 0x16 || head[1] != 0x03 {
		return nil, "", errNotHello
	}
	n := int(binary.BigEndian.Uint16(head[3:5]))
	if n == 0 || n > maxRecord {
		return nil, "", errNotHello
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, "", err
	}
	sni, err := serverName(body)
	if err != nil {
		return nil, "", err
	}
	return append(head, body...), sni, nil
}

// serverName parses a handshake message of type ClientHello and returns the
// host_name of its server_name extension.
func serverName(b []byte) (string, error) {
	if len(b) < 4 || b[0] != 0x01 {
		return "", errNotHello
	}
	b = b[4:] // type and length: the length may be past this record
	// version (2), random (32)
	if len(b) < 34 {
		return "", errNotHello
	}
	b = b[34:]
	skip := func(lenBytes int) bool {
		if len(b) < lenBytes {
			return false
		}
		var n int
		if lenBytes == 1 {
			n = int(b[0])
		} else {
			n = int(binary.BigEndian.Uint16(b[:2]))
		}
		if len(b) < lenBytes+n {
			return false
		}
		b = b[lenBytes+n:]
		return true
	}
	if !skip(1) || !skip(2) || !skip(1) { // session id, cipher suites, compression
		return "", errNotHello
	}
	if len(b) < 2 {
		return "", errNotHello
	}
	ext := b[2:]
	if l := int(binary.BigEndian.Uint16(b[:2])); l < len(ext) {
		ext = ext[:l]
	}
	for len(ext) >= 4 {
		typ := binary.BigEndian.Uint16(ext[:2])
		l := int(binary.BigEndian.Uint16(ext[2:4]))
		if len(ext) < 4+l {
			break
		}
		data := ext[4 : 4+l]
		ext = ext[4+l:]
		if typ != 0 {
			continue
		}
		if len(data) < 2 {
			return "", errNotHello
		}
		list := data[2:]
		for len(list) >= 3 {
			nameType := list[0]
			nl := int(binary.BigEndian.Uint16(list[1:3]))
			if len(list) < 3+nl {
				return "", errNotHello
			}
			if nameType == 0 {
				return string(list[3 : 3+nl]), nil
			}
			list = list[3+nl:]
		}
	}
	return "", errNotHello
}
