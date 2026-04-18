package vaultwarden

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	cryptopkg "github.com/prabinv/1pass-vaultwarden-sync/internal/crypto"
	syncp "github.com/prabinv/1pass-vaultwarden-sync/internal/sync"
)

// Cipher represents a single item (cipher) in the Vaultwarden API.
type Cipher struct {
	ID             string       `json:"Id,omitempty"`
	Name           string       `json:"Name"`
	Type           int          `json:"Type"`
	RevisionDate   string       `json:"RevisionDate,omitempty"`
	OrganizationID *string      `json:"OrganizationId"`
	FolderID       *string      `json:"FolderId"`
	Favorite       bool         `json:"Favorite"`
	Reprompt       int          `json:"Reprompt"`
	Notes          *string      `json:"Notes"`
	Login          *CipherLogin `json:"Login,omitempty"`
}

// CipherLogin holds the login-specific fields of a Cipher (response).
type CipherLogin struct {
	Username string `json:"Username"`
	Password string `json:"Password"`
}

// cipherRequest is the write payload for POST/PUT /api/ciphers.
// Vaultwarden accepts camelCase on input but returns PascalCase in responses.
type cipherRequest struct {
	Type           int                  `json:"type"`
	Name           string               `json:"name"`
	OrganizationID *string              `json:"organizationId"`
	FolderID       *string              `json:"folderId"`
	Favorite       bool                 `json:"favorite"`
	Reprompt       int                  `json:"reprompt"`
	Notes          *string              `json:"notes"`
	Login          *cipherLoginRequest  `json:"login,omitempty"`
}

type cipherLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// ListResponse wraps the Vaultwarden /api/ciphers endpoint response.
type ListResponse struct {
	Data []Cipher `json:"Data"`
}

// MapCipher converts a Vaultwarden Cipher to a canonical sync.Item.
// Field values are used as-is (no decryption); use the Client methods for
// E2E-encrypted production traffic.
func MapCipher(c Cipher) (syncp.Item, error) {
	updatedAt, err := time.Parse(time.RFC3339, c.RevisionDate)
	if err != nil {
		return syncp.Item{}, fmt.Errorf("parsing RevisionDate %q: %w", c.RevisionDate, err)
	}

	fields := make(map[string]string)
	if c.Login != nil {
		fields["username"] = c.Login.Username
		fields["password"] = c.Login.Password
	}

	return syncp.Item{
		ExternalID: c.ID,
		Name:       c.Name,
		Type:       strconv.Itoa(c.Type),
		Fields:     fields,
		UpdatedAt:  updatedAt.UTC(),
	}, nil
}

// Client implements sync.ItemSink against the Vaultwarden REST API.
// When userKey is non-nil, field values are transparently encrypted on write
// and decrypted on read using Bitwarden's AES-256-CBC + HMAC-SHA256 scheme.
type Client struct {
	baseURL    string
	httpClient *http.Client
	token      string
	userKey    *cryptopkg.UserKey // nil = no E2E encryption (test / plaintext mode)

	mu           sync.Mutex            // protects cipherByName
	cipherByName map[string]syncp.Item // populated lazily by ensureCache
}

// New creates a new Vaultwarden Client. token is a Bearer access token obtained
// via the Bitwarden identity endpoint. userKey is the decrypted user symmetric
// key used for E2E field encryption; pass nil to disable encryption (tests).
func New(baseURL, token string, userKey *cryptopkg.UserKey) *Client {
	// Disable HTTP/2: some Vaultwarden deployments (behind nginx/Caddy/Traefik)
	// send RST_STREAM INTERNAL_ERROR mid-response under HTTP/2. Force HTTP/1.1.
	transport := &http.Transport{
		TLSNextProto: make(map[string]func(string, *tls.Conn) http.RoundTripper),
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Transport: transport},
		token:      token,
		userKey:    userKey,
	}
}

// NewTestClient creates a Client pointing at an arbitrary base URL with
// encryption disabled. Intended for use in tests with httptest.Server.
func NewTestClient(baseURL, token string) *Client {
	return New(baseURL, token, nil)
}

// GetItem looks up a cipher by decrypted name. Returns nil if not found.
// The returned Item's ExternalID is the Vaultwarden cipher UUID.
//
// The cipher list is fetched once and cached for the lifetime of the Client
// via WarmCache; call it before a batch of GetItem calls to avoid N network
// round-trips.
func (c *Client) GetItem(ctx context.Context, name string) (*syncp.Item, error) {
	slog.DebugContext(ctx, "looking up cipher by name", slog.String("name", name))

	if err := c.ensureCache(ctx); err != nil {
		return nil, err
	}

	c.mu.Lock()
	item, ok := c.cipherByName[name]
	c.mu.Unlock()

	if !ok {
		slog.DebugContext(ctx, "cipher not found", slog.String("name", name))
		return nil, nil
	}
	return &item, nil
}

// WarmCache pre-fetches and decrypts all cipher names, building an in-memory
// lookup map. Call once before a sync run to avoid per-item network requests.
func (c *Client) WarmCache(ctx context.Context) error {
	slog.InfoContext(ctx, "warming Vaultwarden cipher cache")
	return c.ensureCache(ctx)
}

// ensureCache builds the name→Item cache if it hasn't been built yet.
// The network call is made outside the lock; a double-checked guard prevents
// redundant rebuilds if two goroutines race to initialise.
func (c *Client) ensureCache(ctx context.Context) error {
	c.mu.Lock()
	if c.cipherByName != nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	ciphers, err := c.listCiphers(ctx)
	if err != nil {
		return err
	}
	cache := make(map[string]syncp.Item, len(ciphers))
	for _, cipher := range ciphers {
		decrypted, err := c.decryptField(cipher.Name)
		if err != nil {
			continue // skip undecryptable ciphers
		}
		item, err := c.cipherToItem(cipher)
		if err != nil {
			continue
		}
		cache[decrypted] = item
	}

	c.mu.Lock()
	if c.cipherByName == nil { // guard against concurrent rebuild
		c.cipherByName = cache
	}
	c.mu.Unlock()
	slog.InfoContext(ctx, "cipher cache ready", slog.Int("count", len(cache)))
	return nil
}

// CreateItem creates a new cipher in Vaultwarden.
// Field values are encrypted when a userKey is configured.
func (c *Client) CreateItem(ctx context.Context, item syncp.Item) error {
	slog.InfoContext(ctx, "creating cipher", slog.String("name", item.Name))

	req, err := c.syncItemToRequest(item)
	if err != nil {
		return fmt.Errorf("building cipher for create: %w", err)
	}

	c.mu.Lock()
	c.cipherByName = nil // invalidate cache after write
	c.mu.Unlock()
	if err := c.doJSON(ctx, http.MethodPost, "/api/ciphers", req, nil); err != nil {
		slog.ErrorContext(ctx, "failed to create cipher",
			slog.String("name", item.Name),
			slog.Any("error", err))
		return err
	}
	return nil
}

// UpdateItem replaces an existing cipher in Vaultwarden.
// Field values are encrypted when a userKey is configured.
func (c *Client) UpdateItem(ctx context.Context, item syncp.Item) error {
	slog.InfoContext(ctx, "updating cipher",
		slog.String("name", item.Name),
		slog.String("id", item.ExternalID))

	req, err := c.syncItemToRequest(item)
	if err != nil {
		return fmt.Errorf("building cipher for update: %w", err)
	}

	c.mu.Lock()
	c.cipherByName = nil // invalidate cache after write
	c.mu.Unlock()
	if err := c.doJSON(ctx, http.MethodPut, "/api/ciphers/"+item.ExternalID, req, nil); err != nil {
		slog.ErrorContext(ctx, "failed to update cipher",
			slog.String("name", item.Name),
			slog.Any("error", err))
		return err
	}
	return nil
}

// listCiphers fetches all ciphers from the Vaultwarden API.
func (c *Client) listCiphers(ctx context.Context) ([]Cipher, error) {
	slog.DebugContext(ctx, "listing all ciphers")

	var resp ListResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/ciphers", nil, &resp); err != nil {
		return nil, err
	}

	slog.DebugContext(ctx, "ciphers fetched", slog.Int("count", len(resp.Data)))
	return resp.Data, nil
}

// cipherToItem converts a raw Cipher from the API into a sync.Item,
// decrypting field values when a userKey is configured.
func (c *Client) cipherToItem(raw Cipher) (syncp.Item, error) {
	updatedAt, err := time.Parse(time.RFC3339, raw.RevisionDate)
	if err != nil {
		return syncp.Item{}, fmt.Errorf("parsing RevisionDate %q: %w", raw.RevisionDate, err)
	}

	name, err := c.decryptField(raw.Name)
	if err != nil {
		return syncp.Item{}, fmt.Errorf("decrypting name: %w", err)
	}

	fields := make(map[string]string)
	if raw.Login != nil {
		username, err := c.decryptField(raw.Login.Username)
		if err != nil {
			return syncp.Item{}, fmt.Errorf("decrypting username: %w", err)
		}
		password, err := c.decryptField(raw.Login.Password)
		if err != nil {
			return syncp.Item{}, fmt.Errorf("decrypting password: %w", err)
		}
		fields["username"] = username
		fields["password"] = password
	}

	return syncp.Item{
		ExternalID: raw.ID,
		Name:       name,
		Type:       strconv.Itoa(raw.Type),
		Fields:     fields,
		UpdatedAt:  updatedAt.UTC(),
	}, nil
}

// opCategoryToVWType maps a 1Password item category string to a Vaultwarden
// cipher type integer. Unknown categories default to Login (1).
func opCategoryToVWType(category string) int {
	switch strings.ToUpper(category) {
	case "SECURE_NOTE":
		return 2
	case "CREDIT_CARD":
		return 3
	case "IDENTITY":
		return 4
	default:
		return 1 // Login covers LOGIN, PASSWORD, DATABASE, SERVER, etc.
	}
}

// syncItemToRequest converts a sync.Item to a cipherRequest for writes,
// encrypting field values when a userKey is configured.
func (c *Client) syncItemToRequest(item syncp.Item) (cipherRequest, error) {
	t := opCategoryToVWType(item.Type)

	name, err := c.encryptField(item.Name)
	if err != nil {
		return cipherRequest{}, fmt.Errorf("encrypting name: %w", err)
	}

	req := cipherRequest{
		Type:     t,
		Name:     name,
		Favorite: false,
		Reprompt: 0,
	}

	if t == 1 { // Login type always requires a login object
		username, err := c.encryptField(item.Fields["username"])
		if err != nil {
			return cipherRequest{}, fmt.Errorf("encrypting username: %w", err)
		}
		password, err := c.encryptField(item.Fields["password"])
		if err != nil {
			return cipherRequest{}, fmt.Errorf("encrypting password: %w", err)
		}
		req.Login = &cipherLoginRequest{
			Username: username,
			Password: password,
		}
	}

	return req, nil
}

// encryptField encrypts a plaintext field value using the user key.
// Returns the value unchanged when no userKey is configured (test / plaintext mode).
func (c *Client) encryptField(s string) (string, error) {
	if c.userKey == nil {
		return s, nil
	}
	cs, err := cryptopkg.Encrypt([]byte(s), c.userKey.EncKey, c.userKey.MacKey)
	if err != nil {
		return "", fmt.Errorf("AES encrypt: %w", err)
	}
	return cs.String(), nil
}

// decryptField decrypts a CipherString field value using the user key.
// Returns the value unchanged when no userKey is configured or the value is empty.
func (c *Client) decryptField(s string) (string, error) {
	if c.userKey == nil || s == "" {
		return s, nil
	}
	cs, err := cryptopkg.ParseCipherString(s)
	if err != nil {
		return "", fmt.Errorf("parsing cipher string: %w", err)
	}
	plain, err := cs.Decrypt(c.userKey.EncKey, c.userKey.MacKey)
	if err != nil {
		return "", fmt.Errorf("AES decrypt: %w", err)
	}
	return string(plain), nil
}

// Close flushes idle HTTP connections held by the underlying transport.
// Call once when the Client is no longer needed.
func (c *Client) Close() {
	c.httpClient.CloseIdleConnections()
}

// doJSON executes an HTTP request with an optional JSON body, decoding the
// response into out when provided.
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	slog.DebugContext(ctx, "vault API request",
		slog.String("method", method),
		slog.String("path", path))

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshalling request body: %w", err)
		}
		bodyReader = strings.NewReader(string(b))
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		slog.ErrorContext(ctx, "vault API error",
			slog.String("method", method),
			slog.String("path", path),
			slog.Int("status", resp.StatusCode),
			slog.String("body", strings.TrimSpace(string(body))))
		return fmt.Errorf("unexpected status %d for %s %s: %s", resp.StatusCode, method, path, strings.TrimSpace(string(body)))
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}
	return nil
}
