package vaultwarden_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	syncp "github.com/prabinv/1pass-vaultwarden-sync/internal/sync"
	vw "github.com/prabinv/1pass-vaultwarden-sync/internal/vaultwarden"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// --- item mapping tests ---

func TestMapCipher(t *testing.T) {
	updatedAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		cipher  vw.Cipher
		want    syncp.Item
		wantErr bool
	}{
		{
			name: "login cipher",
			cipher: vw.Cipher{
				ID:       "vw-abc",
				Name:     "GitHub",
				Type:     1,
				RevisionDate: updatedAt.Format(time.RFC3339),
				Login: &vw.CipherLogin{
					Username: "user@example.com",
					Password: "secret",
				},
			},
			want: syncp.Item{
				ExternalID: "vw-abc",
				Name:       "GitHub",
				Type:       "1",
				Fields: map[string]string{
					"username": "user@example.com",
					"password": "secret",
				},
				UpdatedAt: updatedAt,
			},
		},
		{
			name: "cipher with no login",
			cipher: vw.Cipher{
				ID:           "vw-note",
				Name:         "Note",
				Type:         2,
				RevisionDate: updatedAt.Format(time.RFC3339),
			},
			want: syncp.Item{
				ExternalID: "vw-note",
				Name:       "Note",
				Type:       "2",
				Fields:     map[string]string{},
				UpdatedAt:  updatedAt,
			},
		},
		{
			name: "invalid revision date",
			cipher: vw.Cipher{
				ID:           "bad",
				Name:         "Bad",
				RevisionDate: "not-a-date",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := vw.MapCipher(tt.cipher)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MapCipher() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.ExternalID != tt.want.ExternalID {
				t.Errorf("ExternalID = %q, want %q", got.ExternalID, tt.want.ExternalID)
			}
			if got.Name != tt.want.Name {
				t.Errorf("Name = %q, want %q", got.Name, tt.want.Name)
			}
			if !got.UpdatedAt.Equal(tt.want.UpdatedAt) {
				t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, tt.want.UpdatedAt)
			}
			for k, v := range tt.want.Fields {
				if got.Fields[k] != v {
					t.Errorf("Fields[%q] = %q, want %q", k, got.Fields[k], v)
				}
			}
		})
	}
}

// --- HTTP client tests ---

func TestClient_GetItem_Found(t *testing.T) {
	updatedAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	cipher := vw.Cipher{
		ID:           "vw-123",
		Name:         "Test",
		Type:         1,
		RevisionDate: updatedAt.Format(time.RFC3339),
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(vw.ListResponse{Data: []vw.Cipher{cipher}})
	}))
	defer srv.Close()

	client := vw.NewTestClient(srv.URL, "fake-token")
	item, err := client.GetItem(t.Context(), "Test") // look up by name
	if err != nil {
		t.Fatalf("GetItem() error: %v", err)
	}
	if item == nil {
		t.Fatal("GetItem() returned nil, want item")
	}
	if item.ExternalID != "vw-123" {
		t.Errorf("ExternalID = %q, want %q", item.ExternalID, "vw-123")
	}
}

func TestClient_GetItem_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(vw.ListResponse{Data: []vw.Cipher{}})
	}))
	defer srv.Close()

	client := vw.NewTestClient(srv.URL, "fake-token")
	item, err := client.GetItem(t.Context(), "missing")
	if err != nil {
		t.Fatalf("GetItem() error: %v", err)
	}
	if item != nil {
		t.Errorf("GetItem() = %v, want nil", item)
	}
}

func TestClient_GetItem_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := vw.NewTestClient(srv.URL, "fake-token")
	_, err := client.GetItem(t.Context(), "x")
	if err == nil {
		t.Fatal("expected error on server 500, got nil")
	}
}

// --- concurrency tests ---

// TestGetItem_Concurrent verifies that concurrent GetItem calls after WarmCache
// do not race on the internal cipherByName map. go test -race will catch issues.
func TestGetItem_Concurrent(t *testing.T) {
	updatedAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	ciphers := []vw.Cipher{
		{ID: "1", Name: "Alpha", Type: 1, RevisionDate: updatedAt.Format(time.RFC3339)},
		{ID: "2", Name: "Beta", Type: 1, RevisionDate: updatedAt.Format(time.RFC3339)},
		{ID: "3", Name: "Gamma", Type: 1, RevisionDate: updatedAt.Format(time.RFC3339)},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(vw.ListResponse{Data: ciphers})
	}))
	defer srv.Close()

	client := vw.NewTestClient(srv.URL, "fake-token")
	if err := client.WarmCache(t.Context()); err != nil {
		t.Fatalf("WarmCache(): %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			if _, err := client.GetItem(t.Context(), "Alpha"); err != nil {
				t.Errorf("GetItem(): %v", err)
			}
			if _, err := client.GetItem(t.Context(), "missing"); err != nil {
				t.Errorf("GetItem() missing: %v", err)
			}
		}()
	}
	wg.Wait()
}

// --- fuzz ---

func FuzzParseVaultwardenItem(f *testing.F) {
	valid := vw.Cipher{
		ID:           "abc",
		Name:         "Test",
		Type:         1,
		RevisionDate: "2024-01-01T00:00:00Z",
		Login:        &vw.CipherLogin{Username: "u", Password: "p"},
	}
	b, _ := json.Marshal(valid)
	f.Add(b)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"revision_date":"bad"}`))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var c vw.Cipher
		if err := json.Unmarshal(data, &c); err != nil {
			return
		}
		// MapCipher must never panic.
		_, _ = vw.MapCipher(c)
	})
}
