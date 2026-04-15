package vaultwarden

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	syncp "github.com/prabinv/1pass-vaultwarden-sync/internal/sync"
)

// Cipher represents a single item (cipher) in the Vaultwarden API.
type Cipher struct {
	ID           string       `json:"Id"`
	Name         string       `json:"Name"`
	Type         int          `json:"Type"`
	RevisionDate string       `json:"RevisionDate"`
	Login        *CipherLogin `json:"Login,omitempty"`
}

// CipherLogin holds the login-specific fields of a Cipher.
type CipherLogin struct {
	Username string `json:"Username"`
	Password string `json:"Password"`
}

// ListResponse wraps the Vaultwarden /api/ciphers endpoint response.
type ListResponse struct {
	Data []Cipher `json:"Data"`
}

// MapCipher converts a Vaultwarden Cipher to a canonical sync.Item.
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
type Client struct {
	baseURL    string
	httpClient *http.Client
	token      string
}

// New creates a new Vaultwarden Client. token is a Bearer access token
// obtained via the Bitwarden identity endpoint.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{},
		token:      token,
	}
}

// NewTestClient creates a Client pointing at an arbitrary base URL.
// Intended for use in tests with httptest.Server.
func NewTestClient(baseURL, token string) *Client {
	return New(baseURL, token)
}

// GetItem returns the item with the given externalID, or nil if not found.
func (c *Client) GetItem(ctx context.Context, externalID string) (*syncp.Item, error) {
	ciphers, err := c.listCiphers(ctx)
	if err != nil {
		return nil, err
	}

	for _, cipher := range ciphers {
		if cipher.ID == externalID {
			item, err := MapCipher(cipher)
			if err != nil {
				return nil, fmt.Errorf("mapping cipher %s: %w", cipher.ID, err)
			}
			return &item, nil
		}
	}

	return nil, nil
}

// CreateItem creates a new cipher in Vaultwarden.
func (c *Client) CreateItem(ctx context.Context, item syncp.Item) error {
	cipher := syncItemToCipher(item)
	return c.doJSON(ctx, http.MethodPost, "/api/ciphers", cipher, nil)
}

// UpdateItem replaces an existing cipher in Vaultwarden.
func (c *Client) UpdateItem(ctx context.Context, item syncp.Item) error {
	cipher := syncItemToCipher(item)
	return c.doJSON(ctx, http.MethodPut, "/api/ciphers/"+item.ExternalID, cipher, nil)
}

// listCiphers fetches all ciphers from the Vaultwarden API.
func (c *Client) listCiphers(ctx context.Context) ([]Cipher, error) {
	var resp ListResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/ciphers", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// doJSON executes an HTTP request with an optional JSON body, decoding the
// response into out when provided.
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
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
		return fmt.Errorf("unexpected status %d for %s %s", resp.StatusCode, method, path)
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}
	return nil
}

// syncItemToCipher converts a sync.Item to a Vaultwarden Cipher for writes.
func syncItemToCipher(item syncp.Item) Cipher {
	t, _ := strconv.Atoi(item.Type)
	cipher := Cipher{
		ID:   item.ExternalID,
		Name: item.Name,
		Type: t,
	}
	if item.Fields["username"] != "" || item.Fields["password"] != "" {
		cipher.Login = &CipherLogin{
			Username: item.Fields["username"],
			Password: item.Fields["password"],
		}
	}
	return cipher
}
