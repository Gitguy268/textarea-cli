// Package share is textarea's link format, ported from lib/share.js.
//
// A note travels in the URL hash: raw deflate, base64url without padding.
// An encrypted one is "!" and then TXE1 | salt(16) | iv(12) | iterations
// (uint32, big endian) | AES-256-GCM ciphertext and tag, with the key from
// PBKDF2-HMAC-SHA256 over the NFC password. The style half rides after a NUL.
package share

import (
	"bytes"
	"compress/flate"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// The deployed web app links point at.
const AppURL = "https://gitguy268-textarea.vercel.app/"

const (
	EncryptedPrefix   = "!"
	saltLength        = 16
	ivLength          = 12
	tagLength         = 16
	DefaultIterations = 210_000
	maxIterations     = 5_000_000
	maxInflatedBytes  = 64 << 20
	HashLimit         = 80000
)

var magic = []byte("TXE1")

// DecryptError is a wrong password, a truncated link or a tampered one.
type DecryptError struct{ msg string }

func (e *DecryptError) Error() string { return e.msg }

// ToBase64URL encodes without padding.
func ToBase64URL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// FromBase64URL decodes with or without padding.
func FromBase64URL(s string) ([]byte, error) {
	s = strings.TrimRight(strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' {
			return -1
		}
		return r
	}, s), "=")
	return base64.RawURLEncoding.DecodeString(s)
}

// EncodeHash is note -> hash, without the leading '#'.
func EncodeHash(content string) string {
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.BestCompression)
	w.Write([]byte(content))
	w.Close()
	return ToBase64URL(buf.Bytes())
}

// DecodeHash is hash -> note, for an unencrypted hash.
func DecodeHash(hash string) (string, error) {
	raw, err := FromBase64URL(hash)
	if err != nil {
		return "", errors.New("This link is not readable.")
	}
	r := flate.NewReader(bytes.NewReader(raw))
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, maxInflatedBytes+1))
	if err != nil {
		return "", errors.New("This link is not readable.")
	}
	if len(out) > maxInflatedBytes {
		return "", errors.New("This link expands to too much text to open.")
	}
	return string(out), nil
}

func deriveKey(password string, salt []byte, iterations int) []byte {
	key, _ := pbkdf2.Key(sha256.New, norm.NFC.String(password), salt, iterations, 32)
	return key
}

// EncryptHash encrypts a note into a payload without the "!" prefix.
func EncryptHash(content, password string) (string, error) {
	return encrypt(content, password, DefaultIterations)
}

func encrypt(content, password string, iterations int) (string, error) {
	salt := make([]byte, saltLength)
	iv := make([]byte, ivLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	gcm, err := newGCM(deriveKey(password, salt, iterations))
	if err != nil {
		return "", err
	}
	header := append(append(append([]byte{}, magic...), salt...), iv...)
	header = binary.BigEndian.AppendUint32(header, uint32(iterations))
	return ToBase64URL(gcm.Seal(header, iv, []byte(content), nil)), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// DecryptHash decrypts a payload (without the "!").
func DecryptHash(payload, password string) (string, error) {
	b, err := FromBase64URL(payload)
	if err != nil {
		return "", &DecryptError{"This link is not readable."}
	}
	headerLength := len(magic) + saltLength + ivLength + 4
	if len(b) < headerLength+tagLength {
		return "", &DecryptError{"This link is truncated."}
	}
	if !bytes.Equal(b[:len(magic)], magic) {
		return "", &DecryptError{"This link uses an unknown format."}
	}
	salt := b[len(magic) : len(magic)+saltLength]
	iv := b[len(magic)+saltLength : len(magic)+saltLength+ivLength]
	iterations := int(binary.BigEndian.Uint32(b[headerLength-4 : headerLength]))
	if iterations < 1000 || iterations > maxIterations {
		return "", &DecryptError{"This link asks for an implausible amount of work."}
	}
	gcm, err := newGCM(deriveKey(password, salt, iterations))
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, iv, b[headerLength:], nil)
	if err != nil {
		return "", &DecryptError{"Wrong password, or this link has been altered."}
	}
	return string(plain), nil
}

// IsEncryptedHash reports whether a hash (without '#') is encrypted.
func IsEncryptedHash(hash string) bool { return strings.HasPrefix(hash, EncryptedPrefix) }

// SplitContent separates the text from the style half.
func SplitContent(content string) (text, style string) {
	if i := strings.IndexByte(content, 0); i >= 0 {
		return content[:i], content[i+1:]
	}
	return content, ""
}

// JoinContent is the inverse of SplitContent.
func JoinContent(text, style string) string {
	if style != "" {
		return text + "\x00" + style
	}
	return text
}

// Link is the full URL for a note, encrypted when password is set.
func Link(text, style, password string) (string, error) {
	content := JoinContent(text, style)
	if password == "" {
		return AppURL + "#" + EncodeHash(content), nil
	}
	payload, err := EncryptHash(content, password)
	if err != nil {
		return "", err
	}
	return AppURL + "#" + EncryptedPrefix + payload, nil
}

// HashOf pulls the hash out of anything pasted: a full URL, "#…" or bare.
func HashOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '#'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Open decodes a pasted link. needsPassword is true when the link is
// encrypted and password was empty.
func Open(link, password string) (text, style string, needsPassword bool, err error) {
	hash := HashOf(link)
	var content string
	if IsEncryptedHash(hash) {
		if password == "" {
			return "", "", true, nil
		}
		content, err = DecryptHash(hash[1:], password)
	} else {
		content, err = DecodeHash(hash)
	}
	if err != nil {
		return "", "", false, err
	}
	text, style = SplitContent(content)
	return text, style, false, nil
}
