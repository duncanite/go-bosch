package homeconnect

import (
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Key is what opens an appliance: the key of its profile, which the
// owner's Home Connect account gives, and the initial vector that comes
// with it for an appliance of the AES mode. It is a secret, and prints as
// none: under fmt and slog a Key shows that it is one and nothing of it.
type Key struct {
	psk    []byte
	vector []byte
}

// redacted is what a Key shows of itself.
const redacted = "homeconnect.Key(redacted)"

// ParseKey reads a key and its initial vector as a profile writes them:
// base64 of the URL alphabet, with or without padding. An appliance of the
// TLS mode has no vector: its key parses, and Dial says it does not speak
// that mode.
func ParseKey(key, vector string) (Key, error) {
	psk, err := unbase64(key)
	if err != nil || len(psk) == 0 {
		return Key{}, fmt.Errorf("%w: the key is not base64 of the URL alphabet", ErrArgument)
	}

	parsed := Key{psk: psk}

	if vector == "" {
		return parsed, nil
	}

	if parsed.vector, err = unbase64(vector); err != nil || len(parsed.vector) != vectorSize {
		return Key{}, fmt.Errorf("%w: the initial vector is not base64 of the URL alphabet, for %d bytes",
			ErrArgument, vectorSize)
	}

	return parsed, nil
}

// vectorSize is the length of an initial vector: one block of AES.
const vectorSize = 16

func unbase64(text string) ([]byte, error) {
	//nolint:wrapcheck // said by ParseKey, which names what was being read
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(text, "="))
}

// Format shows that it is a key, and nothing of it, under every verb: fmt
// asks String for some of them only, and would print the bytes for the
// rest.
func (Key) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, redacted) }

// String shows that it is a key, and nothing of it.
func (Key) String() string { return redacted }

// GoString shows that it is a key, and nothing of it.
func (Key) GoString() string { return redacted }

// LogValue shows that it is a key, and nothing of it.
func (Key) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalText refuses: a key is written nowhere by accident.
func (Key) MarshalText() ([]byte, error) {
	return nil, fmt.Errorf("%w: a key is not written out", ErrArgument)
}
