package onepassword_test

import (
	"encoding/json"
	"testing"
	"time"

	op "github.com/prabinv/1pass-vaultwarden-sync/internal/onepassword"
	syncp "github.com/prabinv/1pass-vaultwarden-sync/internal/sync"
)

// --- item mapping tests ---

func TestMapItem(t *testing.T) {
	updatedAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		raw     op.RawItem
		want    syncp.Item
		wantErr bool
	}{
		{
			name: "login item with fields",
			raw: op.RawItem{
				ID:        "abc123",
				Title:     "GitHub",
				Category:  "LOGIN",
				UpdatedAt: updatedAt.Format(time.RFC3339),
				Fields: []op.RawField{
					{ID: "username", Label: "username", Value: "user@example.com"},
					{ID: "password", Label: "password", Value: "secret"},
				},
			},
			want: syncp.Item{
				ExternalID: "abc123",
				Name:       "GitHub",
				Type:       "LOGIN",
				Fields: map[string]string{
					"username": "user@example.com",
					"password": "secret",
				},
				UpdatedAt: updatedAt,
			},
		},
		{
			name: "item with no fields",
			raw: op.RawItem{
				ID:        "xyz",
				Title:     "Note",
				Category:  "SECURE_NOTE",
				UpdatedAt: updatedAt.Format(time.RFC3339),
			},
			want: syncp.Item{
				ExternalID: "xyz",
				Name:       "Note",
				Type:       "SECURE_NOTE",
				Fields:     map[string]string{},
				UpdatedAt:  updatedAt,
			},
		},
		{
			name: "invalid timestamp",
			raw: op.RawItem{
				ID:        "bad",
				Title:     "Bad",
				Category:  "LOGIN",
				UpdatedAt: "not-a-date",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := op.MapItem(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MapItem() error = %v, wantErr %v", err, tt.wantErr)
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
			if got.Type != tt.want.Type {
				t.Errorf("Type = %q, want %q", got.Type, tt.want.Type)
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

// --- fuzz ---

func FuzzParseOPItem(f *testing.F) {
	valid := op.RawItem{
		ID:        "abc",
		Title:     "Test",
		Category:  "LOGIN",
		UpdatedAt: "2024-01-01T00:00:00Z",
		Fields:    []op.RawField{{ID: "u", Label: "username", Value: "user"}},
	}
	b, _ := json.Marshal(valid)
	f.Add(b)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"updated_at":"bad-date"}`))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var raw op.RawItem
		if err := json.Unmarshal(data, &raw); err != nil {
			return // invalid JSON — skip
		}
		// MapItem must never panic regardless of content.
		_, _ = op.MapItem(raw)
	})
}
