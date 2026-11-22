// Package aeschain is the encryption of a Home Connect appliance's AES
// mode: every message of a connection encrypted with AES-CBC as one
// stream, and authenticated by an HMAC that covers the one before it. The
// two directions of a connection are two chains, each with its own state.
//
// A chain cannot lose a frame: each one opens only after the one before
// it, so a frame that never comes leaves every later one unreadable. A
// frame that does not authenticate moves nothing, and the one that
// belongs in its place still opens; but whoever reads cannot tell a frame
// altered from one missing or one foreign, and ends the connection.
package aeschain

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// The two directions, as each end marks what it sends.
const (
	// FromClient marks what a client sends.
	FromClient byte = 'E'
	// FromAppliance marks what an appliance sends.
	FromAppliance byte = 'C'
)

// TagSize is the length of the authentication tag that ends a frame.
const TagSize = 16

// ErrKey is a key or an initial vector a chain cannot be made of.
var ErrKey = errors.New("aeschain: bad key")

// ErrTag is a frame whose tag is not the one its sender's key gives: sent
// under another key, altered, or out of its place in the chain.
var ErrTag = errors.New("aeschain: the frame does not authenticate")

// ErrFrame is a frame that is no frame of this encryption: too short, of
// a length that is no multiple of a block, or badly padded.
var ErrFrame = errors.New("aeschain: malformed frame")

// Chain is one direction of a connection: what seals every frame sent
// that way, or what opens every frame received that way. It is used by
// one goroutine at a time, in the order of the frames on the wire.
type Chain struct {
	mode      cipher.BlockMode
	mac       []byte
	vector    []byte
	direction byte
	last      [TagSize]byte
}

// derived is a key made from the pre-shared one, by its label.
func derived(psk []byte, label string) []byte {
	mac := hmac.New(sha256.New, psk)
	mac.Write([]byte(label))

	return mac.Sum(nil)
}

func chain(psk, vector []byte, direction byte, seals bool) (*Chain, error) {
	if len(psk) == 0 {
		return nil, fmt.Errorf("%w: no pre-shared key", ErrKey)
	}

	if len(vector) != aes.BlockSize {
		return nil, fmt.Errorf("%w: an initial vector of %d bytes, want %d", ErrKey, len(vector), aes.BlockSize)
	}

	block, err := aes.NewCipher(derived(psk, "ENC"))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKey, err)
	}

	made := &Chain{mac: derived(psk, "MAC"), vector: append([]byte(nil), vector...), direction: direction}

	if seals {
		made.mode = cipher.NewCBCEncrypter(block, made.vector)
	} else {
		made.mode = cipher.NewCBCDecrypter(block, made.vector)
	}

	return made, nil
}

// Sealer is the chain that seals what is sent in a direction, from the
// pre-shared key and the initial vector of an appliance's profile.
func Sealer(psk, vector []byte, direction byte) (*Chain, error) {
	return chain(psk, vector, direction, true)
}

// Opener is the chain that opens what is received in a direction.
func Opener(psk, vector []byte, direction byte) (*Chain, error) {
	return chain(psk, vector, direction, false)
}

// Seal is the frame that carries a message: the message padded to whole
// blocks, encrypted, then the tag. The padding is the protocol's own: a
// zero, random bytes, and its length in its last byte, never less than
// two bytes.
func (c *Chain) Seal(message []byte) ([]byte, error) {
	padding := aes.BlockSize - len(message)%aes.BlockSize
	if padding == 1 {
		padding += aes.BlockSize
	}

	frame := make([]byte, len(message)+padding, len(message)+padding+TagSize)
	copy(frame, message)

	if _, err := rand.Read(frame[len(message)+1 : len(frame)-1]); err != nil {
		return nil, fmt.Errorf("padding a frame: %w", err)
	}

	frame[len(frame)-1] = byte(padding)

	c.mode.CryptBlocks(frame, frame)

	tag := c.tag(frame)
	copy(c.last[:], tag)

	return append(frame, tag...), nil
}

// Open is the message a frame carries. A frame that does not authenticate
// leaves the chain where it was: the frame that belongs there still
// opens.
func (c *Chain) Open(frame []byte) ([]byte, error) {
	if len(frame) < aes.BlockSize+TagSize || len(frame)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: %d bytes", ErrFrame, len(frame))
	}

	sealed, tag := frame[:len(frame)-TagSize], frame[len(frame)-TagSize:]

	if !hmac.Equal(tag, c.tag(sealed)) {
		return nil, ErrTag
	}

	copy(c.last[:], tag)

	message := make([]byte, len(sealed))
	c.mode.CryptBlocks(message, sealed)

	padding := int(message[len(message)-1])
	if padding < 1 || padding > len(message) {
		return nil, fmt.Errorf("%w: a padding of %d bytes in %d", ErrFrame, padding, len(message))
	}

	return message[:len(message)-padding], nil
}

// tag is the authentication of a frame's encrypted part, after the tag of
// the frame before it.
func (c *Chain) tag(sealed []byte) []byte {
	mac := hmac.New(sha256.New, c.mac)
	mac.Write(c.vector)
	mac.Write([]byte{c.direction})
	mac.Write(c.last[:])
	mac.Write(sealed)

	return mac.Sum(nil)[:TagSize]
}
