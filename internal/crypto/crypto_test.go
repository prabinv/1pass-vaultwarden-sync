package crypto_test

import (
	"crypto/rand"
	"testing"

	"github.com/prabinv/1pass-vaultwarden-sync/internal/crypto"
)

// --- CipherString ---

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	encKey := make([]byte, 32)
	macKey := make([]byte, 32)
	rand.Read(encKey) //nolint:errcheck
	rand.Read(macKey) //nolint:errcheck

	tests := []struct {
		name      string
		plaintext string
	}{
		{"empty", ""},
		{"short", "hi"},
		{"ascii", "secret password 12345!"},
		{"exactly one block", "0123456789abcdef"},   // 16 bytes
		{"two blocks", "0123456789abcdef0123456789abcdef"}, // 32 bytes
		{"unicode", "パスワード🔑"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, err := crypto.Encrypt([]byte(tt.plaintext), encKey, macKey)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}

			got, err := cs.Decrypt(encKey, macKey)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if string(got) != tt.plaintext {
				t.Errorf("roundtrip: got %q, want %q", got, tt.plaintext)
			}

			// Wire-format round-trip via ParseCipherString.
			cs2, err := crypto.ParseCipherString(cs.String())
			if err != nil {
				t.Fatalf("ParseCipherString(%q): %v", cs.String(), err)
			}
			got2, err := cs2.Decrypt(encKey, macKey)
			if err != nil {
				t.Fatalf("Decrypt after parse: %v", err)
			}
			if string(got2) != tt.plaintext {
				t.Errorf("parse+decrypt: got %q, want %q", got2, tt.plaintext)
			}
		})
	}
}

func TestDecrypt_WrongMAC(t *testing.T) {
	encKey := make([]byte, 32)
	macKey := make([]byte, 32)
	rand.Read(encKey) //nolint:errcheck
	rand.Read(macKey) //nolint:errcheck

	cs, _ := crypto.Encrypt([]byte("secret"), encKey, macKey)
	cs.MAC[0] ^= 0xFF // tamper with MAC

	if _, err := cs.Decrypt(encKey, macKey); err == nil {
		t.Error("expected MAC verification failure, got nil")
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	encKey := make([]byte, 32)
	macKey := make([]byte, 32)
	wrongMacKey := make([]byte, 32)
	rand.Read(encKey)      //nolint:errcheck
	rand.Read(macKey)      //nolint:errcheck
	rand.Read(wrongMacKey) //nolint:errcheck

	cs, _ := crypto.Encrypt([]byte("data"), encKey, macKey)

	if _, err := cs.Decrypt(encKey, wrongMacKey); err == nil {
		t.Error("expected error with wrong MAC key, got nil")
	}
}

func TestParseCipherString_Errors(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"wrong type", "0.abc|def|ghi"},
		{"too few parts", "2.abc|def"},
		{"too many parts", "2.abc|def|ghi|extra"},
		{"bad base64 IV", "2.!!!!|AAAA|AAAA"},
		{"bad base64 CT", "2.AAAA|!!!!|AAAA"},
		{"bad base64 MAC", "2.AAAA|AAAA|!!!!"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := crypto.ParseCipherString(tc.input); err == nil {
				t.Errorf("ParseCipherString(%q): expected error, got nil", tc.input)
			}
		})
	}
}

// --- Key derivation ---

func TestDeriveMasterKey_PBKDF2(t *testing.T) {
	params := crypto.KdfParams{Type: crypto.KdfPBKDF2, Iterations: 100_000}
	k1, err := crypto.DeriveMasterKey([]byte("password"), []byte("user@example.com"), params)
	if err != nil {
		t.Fatalf("DeriveMasterKey: %v", err)
	}
	if len(k1) != 32 {
		t.Errorf("expected 32-byte key, got %d", len(k1))
	}
	// Deterministic
	k2, _ := crypto.DeriveMasterKey([]byte("password"), []byte("user@example.com"), params)
	if string(k1) != string(k2) {
		t.Error("KDF not deterministic")
	}
}

func TestDeriveMasterKey_Argon2id(t *testing.T) {
	params := crypto.KdfParams{
		Type: crypto.KdfArgon2id, Iterations: 3,
		Memory: 64 * 1024, Parallelism: 4,
	}
	k, err := crypto.DeriveMasterKey([]byte("password"), []byte("user@example.com"), params)
	if err != nil {
		t.Fatalf("DeriveMasterKey Argon2id: %v", err)
	}
	if len(k) != 32 {
		t.Errorf("expected 32-byte key, got %d", len(k))
	}
}

func TestDeriveMasterKey_UnsupportedKDF(t *testing.T) {
	_, err := crypto.DeriveMasterKey([]byte("pw"), []byte("salt"), crypto.KdfParams{Type: 99})
	if err == nil {
		t.Error("expected error for unsupported KDF type")
	}
}

func TestStretchMasterKey(t *testing.T) {
	masterKey := make([]byte, 32)
	rand.Read(masterKey) //nolint:errcheck

	encKey, macKey, err := crypto.StretchMasterKey(masterKey)
	if err != nil {
		t.Fatalf("StretchMasterKey: %v", err)
	}
	if len(encKey) != 32 {
		t.Errorf("encKey: want 32 bytes, got %d", len(encKey))
	}
	if len(macKey) != 32 {
		t.Errorf("macKey: want 32 bytes, got %d", len(macKey))
	}
	if string(encKey) == string(macKey) {
		t.Error("encKey and macKey must differ")
	}
	// Deterministic
	enc2, mac2, _ := crypto.StretchMasterKey(masterKey)
	if string(encKey) != string(enc2) || string(macKey) != string(mac2) {
		t.Error("StretchMasterKey not deterministic")
	}
}

func TestDecryptUserKey(t *testing.T) {
	encKey := make([]byte, 32)
	macKey := make([]byte, 32)
	userKeyRaw := make([]byte, 64)
	rand.Read(encKey)     //nolint:errcheck
	rand.Read(macKey)     //nolint:errcheck
	rand.Read(userKeyRaw) //nolint:errcheck

	cs, err := crypto.Encrypt(userKeyRaw, encKey, macKey)
	if err != nil {
		t.Fatalf("Encrypt user key: %v", err)
	}

	uk, err := crypto.DecryptUserKey(cs.String(), encKey, macKey)
	if err != nil {
		t.Fatalf("DecryptUserKey: %v", err)
	}
	if string(uk.EncKey) != string(userKeyRaw[:32]) {
		t.Error("EncKey mismatch")
	}
	if string(uk.MacKey) != string(userKeyRaw[32:]) {
		t.Error("MacKey mismatch")
	}
}

// --- Fuzz ---

func FuzzParseCipherString(f *testing.F) {
	f.Add("2.AAAAAAAAAAAAAAAAAAAAAA==|AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=|AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	f.Add("2.abc|def|ghi")
	f.Add("")
	f.Add("0.abc|def|ghi")
	f.Add("2.abc|def")
	f.Add("notacipher")

	f.Fuzz(func(t *testing.T, s string) {
		cs, err := crypto.ParseCipherString(s)
		if err != nil {
			return // invalid input is expected and fine
		}
		// Valid parse → String() must produce a re-parseable value.
		s2 := cs.String()
		if _, err := crypto.ParseCipherString(s2); err != nil {
			t.Errorf("re-parse of valid CipherString failed: %v\noriginal: %q\nre-encoded: %q", err, s, s2)
		}
	})
}
