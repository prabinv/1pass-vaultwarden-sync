package crypto

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// tokenResponse is the JSON body returned by POST /identity/connect/token.
type tokenResponse struct {
	AccessToken    string `json:"access_token"`
	Key            string `json:"Key"`
	Kdf            int    `json:"Kdf"`
	KdfIterations  int    `json:"KdfIterations"`
	KdfMemory      *int   `json:"KdfMemory"`
	KdfParallelism *int   `json:"KdfParallelism"`
}

// profileResponse is a partial representation of GET /api/accounts/profile.
type profileResponse struct {
	Email string `json:"Email"`
}

// Exchange performs the Bitwarden/Vaultwarden client-credentials token exchange
// and derives the user's symmetric encryption key from the master password.
//
// Steps:
//  1. POST /identity/connect/token → access_token, encrypted Key, KDF params
//  2. GET  /api/accounts/profile   → email (used as KDF salt)
//  3. Derive master key: KDF(masterPassword, email, params)
//  4. Stretch master key via HKDF-Expand → stretched enc/mac key pair
//  5. Decrypt the server Key → user enc/mac key pair
//
// The returned accessToken is a Bearer token for subsequent API calls.
// The returned UserKey is used to encrypt and decrypt vault item fields.
func Exchange(
	ctx context.Context,
	httpClient *http.Client,
	baseURL, clientID, clientSecret, masterPassword string,
) (accessToken string, userKey *UserKey, err error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	base := strings.TrimRight(baseURL, "/")

	slog.InfoContext(ctx, "starting identity token exchange",
		slog.String("base_url", base))

	tok, err := postToken(ctx, httpClient, base, clientID, clientSecret)
	if err != nil {
		slog.ErrorContext(ctx, "token exchange failed", slog.Any("error", err))
		return "", nil, err
	}

	kdfName := "PBKDF2-SHA256"
	if tok.Kdf == int(KdfArgon2id) {
		kdfName = "Argon2id"
	}
	slog.DebugContext(ctx, "token received, deriving master key",
		slog.String("kdf", kdfName),
		slog.Int("iterations", tok.KdfIterations))

	email, err := fetchEmail(ctx, httpClient, base, tok.AccessToken)
	if err != nil {
		slog.ErrorContext(ctx, "failed to fetch account profile", slog.Any("error", err))
		return "", nil, err
	}

	mem, par := 0, 0
	if tok.KdfMemory != nil {
		mem = *tok.KdfMemory
	}
	if tok.KdfParallelism != nil {
		par = *tok.KdfParallelism
	}

	masterKey, err := DeriveMasterKey(
		[]byte(masterPassword), []byte(email),
		KdfParams{Type: KdfType(tok.Kdf), Iterations: tok.KdfIterations, Memory: mem, Parallelism: par},
	)
	if err != nil {
		return "", nil, fmt.Errorf("deriving master key: %w", err)
	}

	encKey, macKey, err := StretchMasterKey(masterKey)
	if err != nil {
		return "", nil, fmt.Errorf("stretching master key: %w", err)
	}

	uk, err := DecryptUserKey(tok.Key, encKey, macKey)
	if err != nil {
		slog.ErrorContext(ctx, "failed to decrypt user key — check master password",
			slog.Any("error", err))
		return "", nil, fmt.Errorf("decrypting user key: %w", err)
	}

	slog.InfoContext(ctx, "identity exchange complete — user key ready")
	return tok.AccessToken, uk, nil
}

func postToken(ctx context.Context, c *http.Client, base, clientID, clientSecret string) (*tokenResponse, error) {
	deviceID, err := newDeviceID()
	if err != nil {
		return nil, fmt.Errorf("generating device ID: %w", err)
	}

	// Vaultwarden requires device_type / device_identifier / device_name in
	// addition to the OAuth2 client-credentials fields. Without them the
	// server returns HTTP 400. device_type 21 = SDK (programmatic client).
	form := url.Values{
		"grant_type":        {"client_credentials"},
		"client_id":         {clientID},
		"client_secret":     {clientSecret},
		"scope":             {"api"},
		"device_type":       {"21"},
		"device_identifier": {deviceID},
		"device_name":       {"1pass-vaultwarden-sync"},
		"device_push_token": {""},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/identity/connect/token",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("posting to identity endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("identity endpoint returned HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in identity response")
	}
	if tok.Key == "" {
		return nil, fmt.Errorf("empty Key in identity response — " +
			"ensure VAULTWARDEN_MASTER_PASSWORD is set correctly")
	}
	return &tok, nil
}

// newDeviceID generates a random UUID v4 to identify this client to Vaultwarden.
// A new ID per session is acceptable; Vaultwarden uses it for device tracking only.
func newDeviceID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func fetchEmail(ctx context.Context, c *http.Client, base, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"/api/accounts/profile", nil)
	if err != nil {
		return "", fmt.Errorf("building profile request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching account profile: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("profile endpoint returned HTTP %d", resp.StatusCode)
	}

	var profile profileResponse
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return "", fmt.Errorf("decoding profile response: %w", err)
	}
	if profile.Email == "" {
		return "", fmt.Errorf("empty email in profile response")
	}
	return profile.Email, nil
}
