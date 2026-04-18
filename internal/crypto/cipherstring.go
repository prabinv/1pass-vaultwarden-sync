package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// CipherString is a Bitwarden AES-256-CBC + HMAC-SHA256 encrypted value.
//
// Wire format: "2.<base64(IV)>|<base64(ciphertext)>|<base64(MAC)>"
type CipherString struct {
	IV         []byte
	Ciphertext []byte
	MAC        []byte
}

// ParseCipherString parses a Bitwarden CipherString. Only type-2
// (AES-256-CBC + HMAC-SHA256) is supported.
//
// Wire format: "2.{base64(IV)}|{base64(ciphertext)}|{base64(MAC)}"
// The '.' separates only the type prefix from the data; '|' separates all
// three data fields.
func ParseCipherString(s string) (CipherString, error) {
	if !strings.HasPrefix(s, "2.") {
		return CipherString{}, fmt.Errorf("unsupported CipherString type in %q (only type 2 supported)", s)
	}
	rest := s[2:]

	parts := strings.Split(rest, "|")
	if len(parts) != 3 {
		return CipherString{}, fmt.Errorf("CipherString: expected 3 '|'-separated fields, got %d", len(parts))
	}

	iv, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return CipherString{}, fmt.Errorf("CipherString: decoding IV: %w", err)
	}
	ct, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return CipherString{}, fmt.Errorf("CipherString: decoding ciphertext: %w", err)
	}
	mac, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return CipherString{}, fmt.Errorf("CipherString: decoding MAC: %w", err)
	}

	return CipherString{IV: iv, Ciphertext: ct, MAC: mac}, nil
}

// String returns the wire-format representation.
func (cs CipherString) String() string {
	return "2." +
		base64.StdEncoding.EncodeToString(cs.IV) + "|" +
		base64.StdEncoding.EncodeToString(cs.Ciphertext) + "|" +
		base64.StdEncoding.EncodeToString(cs.MAC)
}

// Decrypt verifies the HMAC and decrypts the ciphertext using AES-256-CBC.
// encKey must be 32 bytes; macKey must be at least 1 byte.
func (cs CipherString) Decrypt(encKey, macKey []byte) ([]byte, error) {
	if len(encKey) != 32 {
		return nil, fmt.Errorf("encKey must be 32 bytes, got %d", len(encKey))
	}
	if len(cs.IV) != aes.BlockSize {
		return nil, fmt.Errorf("IV must be %d bytes, got %d", aes.BlockSize, len(cs.IV))
	}
	if len(cs.Ciphertext) == 0 || len(cs.Ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext length %d is not a non-zero multiple of %d", len(cs.Ciphertext), aes.BlockSize)
	}

	// Verify HMAC-SHA256(macKey, IV || CT) before decrypting (encrypt-then-MAC).
	h := hmac.New(sha256.New, macKey)
	h.Write(cs.IV)
	h.Write(cs.Ciphertext)
	if !hmac.Equal(h.Sum(nil), cs.MAC) {
		return nil, errors.New("CipherString: MAC verification failed")
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, fmt.Errorf("creating AES cipher: %w", err)
	}

	dst := make([]byte, len(cs.Ciphertext))
	cipher.NewCBCDecrypter(block, cs.IV).CryptBlocks(dst, cs.Ciphertext)

	return unpadPKCS7(dst)
}

// Encrypt encrypts plaintext with AES-256-CBC and authenticates with HMAC-SHA256.
// encKey must be 32 bytes; macKey must be at least 1 byte.
func Encrypt(plaintext, encKey, macKey []byte) (CipherString, error) {
	if len(encKey) != 32 {
		return CipherString{}, fmt.Errorf("encKey must be 32 bytes, got %d", len(encKey))
	}

	padded := padPKCS7(plaintext, aes.BlockSize)

	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return CipherString{}, fmt.Errorf("generating IV: %w", err)
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return CipherString{}, fmt.Errorf("creating AES cipher: %w", err)
	}

	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)

	h := hmac.New(sha256.New, macKey)
	h.Write(iv)
	h.Write(ct)

	return CipherString{IV: iv, Ciphertext: ct, MAC: h.Sum(nil)}, nil
}

func padPKCS7(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

func unpadPKCS7(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty data")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(data) {
		return nil, fmt.Errorf("invalid PKCS7 padding byte: %d", pad)
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, errors.New("invalid PKCS7 padding")
		}
	}
	return data[:len(data)-pad], nil
}
