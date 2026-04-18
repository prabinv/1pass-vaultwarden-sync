package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/pbkdf2"
)

// KdfType identifies the key derivation function used by the server.
type KdfType int

const (
	KdfPBKDF2   KdfType = 0
	KdfArgon2id KdfType = 1
)

// KdfParams holds parameters for master key derivation, as returned by the
// identity endpoint.
type KdfParams struct {
	Type        KdfType
	Iterations  int
	Memory      int // KiB, Argon2id only
	Parallelism int // Argon2id only
}

// UserKey holds the user's decrypted symmetric key split into enc and mac halves.
// EncKey (32 bytes) is used for AES-256-CBC; MacKey (32 bytes) for HMAC-SHA256.
type UserKey struct {
	EncKey []byte
	MacKey []byte
}

// DeriveMasterKey derives a 32-byte master key from password and email (the
// KDF salt) using either PBKDF2-SHA256 or Argon2id.
func DeriveMasterKey(password, email []byte, p KdfParams) ([]byte, error) {
	switch p.Type {
	case KdfPBKDF2:
		iter := p.Iterations
		if iter <= 0 {
			iter = 600_000
		}
		return pbkdf2.Key(password, email, iter, 32, sha256.New), nil

	case KdfArgon2id:
		mem := uint32(p.Memory)
		if mem == 0 {
			mem = 64 * 1024
		}
		par := uint8(p.Parallelism)
		if par == 0 {
			par = 4
		}
		itr := uint32(p.Iterations)
		if itr == 0 {
			itr = 3
		}
		return argon2.IDKey(password, email, itr, mem, par, 32), nil

	default:
		return nil, fmt.Errorf("unsupported KDF type: %d", p.Type)
	}
}

// StretchMasterKey derives two 32-byte subkeys from the master key using
// HKDF-Expand (RFC 5869 §2.3) with SHA-256 and "enc"/"mac" as the info
// parameter, mirroring the Bitwarden web client's stretchKey implementation.
func StretchMasterKey(masterKey []byte) (encKey, macKey []byte, err error) {
	encKey = hkdfExpand(masterKey, "enc")
	macKey = hkdfExpand(masterKey, "mac")
	return encKey, macKey, nil
}

// DecryptUserKey parses the encrypted user key CipherString returned by the
// identity endpoint, decrypts it with the stretched master key halves, and
// returns the user's symmetric key split into enc (bytes 0–31) and mac
// (bytes 32–63) halves.
func DecryptUserKey(encryptedKey string, encKey, macKey []byte) (*UserKey, error) {
	cs, err := ParseCipherString(encryptedKey)
	if err != nil {
		return nil, fmt.Errorf("parsing encrypted user key: %w", err)
	}
	raw, err := cs.Decrypt(encKey, macKey)
	if err != nil {
		return nil, fmt.Errorf("decrypting user key: %w", err)
	}
	if len(raw) != 64 {
		return nil, fmt.Errorf("expected 64-byte user key, got %d bytes", len(raw))
	}
	return &UserKey{EncKey: raw[:32], MacKey: raw[32:]}, nil
}

// hkdfExpand implements HKDF-Expand (RFC 5869 §2.3) for a single 32-byte
// output block using HMAC-SHA256. This is sufficient because encKey and macKey
// are each 32 bytes (equal to SHA-256's output size), so N=1.
//
// T(1) = HMAC-Hash(PRK, "" || info || 0x01)
func hkdfExpand(prk []byte, info string) []byte {
	h := hmac.New(sha256.New, prk)
	h.Write([]byte(info))
	h.Write([]byte{0x01})
	return h.Sum(nil)
}
