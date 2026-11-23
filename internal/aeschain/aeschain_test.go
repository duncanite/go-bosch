package aeschain_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/duncanite/go-bosch/internal/aeschain"
)

// The key and the initial vector of these tests: made up.
func keys(t *testing.T) (psk, vector []byte) {
	t.Helper()

	return unhex(t, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"),
		unhex(t, "6465666768696a6b6c6d6e6f70717273")
}

func unhex(t *testing.T, text string) []byte {
	t.Helper()

	raw, err := hex.DecodeString(text)
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

// TestOpensWhatOpenSSLSealed: two frames in a row as an appliance sends
// them, made with OpenSSL's AES-256-CBC and HMAC-SHA256 and none of this
// package: one stream of encryption across both, each tag over the one
// before. The second message is fifteen bytes, the length whose padding
// takes a block of its own.
func TestOpensWhatOpenSSLSealed(t *testing.T) {
	t.Parallel()

	psk, vector := keys(t)

	opener, err := aeschain.Opener(psk, vector, aeschain.FromAppliance)
	if err != nil {
		t.Fatal(err)
	}

	for _, row := range []struct{ frame, want string }{
		{
			"6be0f58960da3018ef4cb4656792cbebe03cc49b67e22e17493adefd5ce64fa90a8c22f2aecfc66b2b8b42982cf98bb2",
			`{"sID":1,"msgID":2}`,
		},
		{
			"ad1044804561880dbb107b094b7a3448c984c64efab0377638dbc29a6ccf1aec632bad318e9a8b3d5391f3426089dfdf",
			"fifteen bytes!!",
		},
	} {
		message, openErr := opener.Open(unhex(t, row.frame))
		if openErr != nil || string(message) != row.want {
			t.Fatalf("Open = %q, %v; want %q", message, openErr, row.want)
		}
	}
}

// TestSealedIsOpened: what one end seals the other opens, message after
// message, whatever the length; a frame is whole blocks and a tag, with
// two bytes of padding at the least.
func TestSealedIsOpened(t *testing.T) {
	t.Parallel()

	psk, vector := keys(t)

	sealer, err := aeschain.Sealer(psk, vector, aeschain.FromClient)
	if err != nil {
		t.Fatal(err)
	}

	opener, err := aeschain.Opener(psk, vector, aeschain.FromClient)
	if err != nil {
		t.Fatal(err)
	}

	for length := range 70 {
		message := bytes.Repeat([]byte{'m'}, length)

		frame, sealErr := sealer.Seal(message)
		if sealErr != nil {
			t.Fatal(sealErr)
		}

		padded := len(frame) - aeschain.TagSize
		if padded%16 != 0 || padded < length+2 || padded > length+17 {
			t.Fatalf("a message of %d bytes is sealed in %d and a tag", length, padded)
		}

		opened, openErr := opener.Open(frame)
		if openErr != nil || !bytes.Equal(opened, message) {
			t.Fatalf("a message of %d bytes opens as %q, %v", length, opened, openErr)
		}
	}
}

// TestWhatDoesNotOpen: a frame of the other direction, of another key, of
// another vector, altered, or out of its place does not authenticate; one
// that is no frame is said to be none. Neither a key nor a vector that
// cannot be one makes a chain.
func TestWhatDoesNotOpen(t *testing.T) {
	t.Parallel()

	psk, vector := keys(t)

	seal := func(psk, vector []byte, direction byte, count int) [][]byte {
		sealer, err := aeschain.Sealer(psk, vector, direction)
		if err != nil {
			t.Fatal(err)
		}

		frames := make([][]byte, 0, count)

		for range count {
			frame, sealErr := sealer.Seal([]byte("a message"))
			if sealErr != nil {
				t.Fatal(sealErr)
			}

			frames = append(frames, frame)
		}

		return frames
	}

	other := append([]byte{0xff}, psk[1:]...)
	elsewhere := append([]byte{0xff}, vector[1:]...)

	good := seal(psk, vector, aeschain.FromAppliance, 2)
	altered := append([]byte(nil), good[0]...)
	altered[3] ^= 1

	for name, frame := range map[string][]byte{
		"the other direction": seal(psk, vector, aeschain.FromClient, 1)[0],
		"another key":         seal(other, vector, aeschain.FromAppliance, 1)[0],
		"another vector":      seal(psk, elsewhere, aeschain.FromAppliance, 1)[0],
		"an altered frame":    altered,
		"the second, first":   good[1],
	} {
		opener, err := aeschain.Opener(psk, vector, aeschain.FromAppliance)
		if err != nil {
			t.Fatal(err)
		}

		if _, err = opener.Open(frame); !errors.Is(err, aeschain.ErrTag) {
			t.Errorf("%s: Open = %v, want ErrTag", name, err)
		}

		// What failed moved nothing: the frame in its place still opens.
		if message, openErr := opener.Open(good[0]); openErr != nil || string(message) != "a message" {
			t.Errorf("%s: the first frame after it opens as %q, %v", name, message, openErr)
		}
	}

	opener, err := aeschain.Opener(psk, vector, aeschain.FromAppliance)
	if err != nil {
		t.Fatal(err)
	}

	for name, frame := range map[string][]byte{
		"nothing":            nil,
		"a tag alone":        make([]byte, 16),
		"no whole blocks":    make([]byte, 40),
		"a block and no tag": make([]byte, 31),
	} {
		if _, err = opener.Open(frame); !errors.Is(err, aeschain.ErrFrame) {
			t.Errorf("%s: Open = %v, want ErrFrame", name, err)
		}
	}

	if _, err = aeschain.Sealer(nil, vector, aeschain.FromClient); !errors.Is(err, aeschain.ErrKey) {
		t.Errorf("a chain of no key: %v, want ErrKey", err)
	}

	if _, err = aeschain.Opener(psk, vector[:15], aeschain.FromClient); !errors.Is(err, aeschain.ErrKey) {
		t.Errorf("a chain of a short vector: %v, want ErrKey", err)
	}
}
