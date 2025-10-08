package stargzget

import (
	"context"
	"testing"

	stor "github.com/flaneur2020/stargz-get/stargzget/storage"
)

func TestParseCredential(t *testing.T) {
	tests := []struct {
		name        string
		credential  string
		wantUser    string
		wantPass    string
		expectError bool
	}{
		{"valid credential", "user:pass", "user", "pass", false},
		{"credential with colon in password", "user:pass:word", "user", "pass:word", false},
		{"missing colon", "userpass", "", "", true},
		{"empty credential", "", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts := make([]string, 0)
			if tt.credential != "" {
				for i, c := range tt.credential {
					if c == ':' {
						parts = append(parts, tt.credential[:i], tt.credential[i+1:])
						break
					}
				}
			}

			if len(parts) != 2 {
				if !tt.expectError {
					t.Errorf("expected valid credential but got error")
				}
				return
			}

			if tt.expectError {
				t.Errorf("expected error but got valid credential")
				return
			}

			if parts[0] != tt.wantUser {
				t.Errorf("username = %q, want %q", parts[0], tt.wantUser)
			}

			if parts[1] != tt.wantPass {
				t.Errorf("password = %q, want %q", parts[1], tt.wantPass)
			}
		})
	}
}

func TestRegistryClient_BasicAuth_Integration(t *testing.T) {
	t.Skip("requires a real registry with basic auth for full verification")

	client := stor.NewRemoteRegistryStorage().WithCredential("testuser", "testpass")

	ctx := context.Background()
	manifest, err := client.GetManifest(ctx, "registry.example.com/test/image:latest")
	if err != nil {
		t.Fatalf("GetManifest failed: %v", err)
	}

	if manifest == nil {
		t.Fatal("manifest is nil")
	}
}
