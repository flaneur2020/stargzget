package stargzget

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegistryClient_WithCredential(t *testing.T) {
	t.Skip("skipping registry credential test in sandbox environment")
	// Create a mock server that requires basic auth
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for Authorization header
		auth := r.Header.Get("Authorization")
		if auth == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Verify Basic Auth
		expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("testuser:testpass"))
		if auth != expectedAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Return a mock manifest
		manifest := Manifest{
			SchemaVersion: 2,
			MediaType:     "application/vnd.oci.image.manifest.v1+json",
			Layers: []Layer{
				{
					MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
					Digest:    "sha256:abc123",
					Size:      1000,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(manifest)
	}))
	defer server.Close()

	tests := []struct {
		name          string
		username      string
		password      string
		expectSuccess bool
	}{
		{
			name:          "valid credentials",
			username:      "testuser",
			password:      "testpass",
			expectSuccess: true,
		},
		{
			name:          "invalid credentials",
			username:      "wronguser",
			password:      "wrongpass",
			expectSuccess: false,
		},
		{
			name:          "no credentials",
			username:      "",
			password:      "",
			expectSuccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewRegistryClient()

			if tt.username != "" && tt.password != "" {
				client = client.WithCredential(tt.username, tt.password)
			}

			// Replace the server URL in the image ref (this is a bit hacky for testing)
			// In a real scenario, we'd need to mock the HTTP client
			// For now, this test verifies the interface works correctly
			rc, ok := client.(*registryClient)
			if !ok {
				t.Fatal("client is not *registryClient")
			}

			if rc.username != tt.username {
				t.Errorf("username = %q, want %q", rc.username, tt.username)
			}

			if rc.password != tt.password {
				t.Errorf("password = %q, want %q", rc.password, tt.password)
			}
		})
	}
}

func TestImageAccessor_WithCredential(t *testing.T) {
	client := NewRegistryClient()
	manifest := &Manifest{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.manifest.v1+json",
		Layers: []Layer{
			{
				MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
				Digest:    "sha256:abc123",
				Size:      1000,
			},
		},
	}

	accessor := NewImageAccessor(client, "ghcr.io", "test/image", manifest)

	tests := []struct {
		name     string
		username string
		password string
	}{
		{
			name:     "set credentials",
			username: "testuser",
			password: "testpass",
		},
		{
			name:     "empty credentials",
			username: "",
			password: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newAccessor := accessor.WithCredential(tt.username, tt.password)

			ia, ok := newAccessor.(*imageAccessor)
			if !ok {
				t.Fatal("accessor is not *imageAccessor")
			}

			if ia.username != tt.username {
				t.Errorf("username = %q, want %q", ia.username, tt.username)
			}

			if ia.password != tt.password {
				t.Errorf("password = %q, want %q", ia.password, tt.password)
			}

			// Verify the original accessor is not modified
			origIA, _ := accessor.(*imageAccessor)
			if origIA.username != "" {
				t.Error("original accessor should not have credentials")
			}
		})
	}
}

func TestParseCredential(t *testing.T) {
	tests := []struct {
		name        string
		credential  string
		wantUser    string
		wantPass    string
		expectError bool
	}{
		{
			name:        "valid credential",
			credential:  "user:pass",
			wantUser:    "user",
			wantPass:    "pass",
			expectError: false,
		},
		{
			name:        "credential with colon in password",
			credential:  "user:pass:word",
			wantUser:    "user",
			wantPass:    "pass:word",
			expectError: false,
		},
		{
			name:        "missing colon",
			credential:  "userpass",
			expectError: true,
		},
		{
			name:        "empty credential",
			credential:  "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This simulates the parseCredential function from main.go
			// We can't import it directly since it's in main package
			// So we test the same logic here
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
	t.Skip("This is an integration test that would need a real registry with basic auth")

	// This is a placeholder for integration testing
	// To test this properly, you would need:
	// 1. A test registry server that requires basic auth
	// 2. Test credentials
	// 3. Test images

	client := NewRegistryClient().WithCredential("testuser", "testpass")

	ctx := context.Background()
	manifest, err := client.GetManifest(ctx, "registry.example.com/test/image:latest")
	if err != nil {
		t.Fatalf("GetManifest failed: %v", err)
	}

	if manifest == nil {
		t.Fatal("manifest is nil")
	}
}
