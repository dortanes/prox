// Package dispatcher implements L4 TCP dispatching based on TLS SNI.
//
// When a service has routes with action type "pass", the dispatcher
// intercepts connections before TLS termination. It peeks at the
// TLS ClientHello to extract the SNI hostname, matches it against
// configured domain patterns, and either relays the raw TCP connection
// to the upstream (for "pass" routes) or feeds it to the HTTP server
// (for regular L7 routes).
package dispatcher

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	recordTypeHandshake      = 22
	handshakeTypeClientHello = 1
	extensionServerName      = 0
	sniHostNameType          = 0

	maxTLSRecordLen = 16384
)

// PeekSNI reads the TLS ClientHello from a connection and extracts the
// SNI hostname. Returns the SNI (empty if not found) and all bytes read
// (for replay to upstream or TLS server). Does not consume the connection
// beyond the first TLS record.
func PeekSNI(r io.Reader) (sni string, buf []byte, err error) {
	// TLS record header: type(1) + version(2) + length(2) = 5 bytes.
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return "", nil, fmt.Errorf("reading TLS record header: %w", err)
	}
	buf = append(buf, header[:]...)

	if header[0] != recordTypeHandshake {
		return "", buf, nil // not a TLS handshake — return buffered bytes, no error
	}

	payloadLen := int(binary.BigEndian.Uint16(header[3:5]))
	if payloadLen > maxTLSRecordLen {
		return "", buf, fmt.Errorf("TLS record too large: %d bytes", payloadLen)
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return "", buf, fmt.Errorf("reading TLS record payload: %w", err)
	}
	buf = append(buf, payload...)

	sni = parseClientHelloSNI(payload)
	return sni, buf, nil
}

// parseClientHelloSNI extracts the SNI from a TLS handshake payload.
// Returns empty string if the ClientHello has no SNI extension.
func parseClientHelloSNI(data []byte) string {
	if len(data) < 4 {
		return ""
	}

	// Handshake header: type(1) + length(3).
	if data[0] != handshakeTypeClientHello {
		return ""
	}
	handshakeLen := int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	data = data[4:]
	if len(data) < handshakeLen {
		return ""
	}
	data = data[:handshakeLen]

	// ClientHello: version(2) + random(32) = 34 bytes.
	if len(data) < 34 {
		return ""
	}
	data = data[34:]

	// Session ID: length(1) + variable.
	if len(data) < 1 {
		return ""
	}
	sessIDLen := int(data[0])
	data = data[1:]
	if len(data) < sessIDLen {
		return ""
	}
	data = data[sessIDLen:]

	// Cipher suites: length(2) + variable.
	if len(data) < 2 {
		return ""
	}
	csLen := int(binary.BigEndian.Uint16(data[:2]))
	data = data[2:]
	if len(data) < csLen {
		return ""
	}
	data = data[csLen:]

	// Compression methods: length(1) + variable.
	if len(data) < 1 {
		return ""
	}
	compLen := int(data[0])
	data = data[1:]
	if len(data) < compLen {
		return ""
	}
	data = data[compLen:]

	// Extensions: length(2) + variable.
	if len(data) < 2 {
		return ""
	}
	extLen := int(binary.BigEndian.Uint16(data[:2]))
	data = data[2:]
	if len(data) < extLen {
		return ""
	}
	data = data[:extLen]

	return findSNIExtension(data)
}

// findSNIExtension walks TLS extensions and extracts the SNI hostname.
func findSNIExtension(data []byte) string {
	for len(data) >= 4 {
		extType := binary.BigEndian.Uint16(data[:2])
		extLen := int(binary.BigEndian.Uint16(data[2:4]))
		data = data[4:]

		if len(data) < extLen {
			return ""
		}

		if extType == extensionServerName {
			return parseSNIExtensionPayload(data[:extLen])
		}

		data = data[extLen:]
	}
	return ""
}

// parseSNIExtensionPayload parses the server_name extension value.
// Format: list_length(2), then entries of: type(1) + name_length(2) + name.
func parseSNIExtensionPayload(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	listLen := int(binary.BigEndian.Uint16(data[:2]))
	data = data[2:]
	if len(data) < listLen {
		return ""
	}
	data = data[:listLen]

	for len(data) >= 3 {
		nameType := data[0]
		nameLen := int(binary.BigEndian.Uint16(data[1:3]))
		data = data[3:]
		if len(data) < nameLen {
			return ""
		}
		if nameType == sniHostNameType {
			return string(data[:nameLen])
		}
		data = data[nameLen:]
	}
	return ""
}
