package stargzget

import (
	"errors"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
)

func TestStargzError_Error(t *testing.T) {
	tests := []struct {
		name    string
		err     *StargzError
		wantStr string
	}{
		{
			name: "basic error",
			err: &StargzError{
				Code:    "TEST_ERROR",
				Message: "test message",
			},
			wantStr: "[TEST_ERROR] test message",
		},
		{
			name: "error with cause",
			err: &StargzError{
				Code:    "TEST_ERROR",
				Message: "test message",
				Cause:   errors.New("underlying error"),
			},
			wantStr: "[TEST_ERROR] test message: underlying error",
		},
		{
			name: "error with details",
			err: &StargzError{
				Code:    "TEST_ERROR",
				Message: "test message",
				Details: map[string]interface{}{"key": "value"},
			},
			wantStr: "details",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if !strings.Contains(got, tt.wantStr) {
				t.Errorf("Error() = %q, want to contain %q", got, tt.wantStr)
			}
		})
	}
}

func TestStargzError_WithCause(t *testing.T) {
	cause := errors.New("root cause")
	err := ErrBlobNotFound.WithCause(cause)

	if err.Cause != cause {
		t.Errorf("WithCause() cause = %v, want %v", err.Cause, cause)
	}

	if !errors.Is(err, cause) {
		t.Error("WithCause() should allow errors.Is to work")
	}
}

func TestStargzError_WithDetail(t *testing.T) {
	err := ErrFileNotFound.WithDetail("path", "/bin/echo")

	if err.Details["path"] != "/bin/echo" {
		t.Errorf("WithDetail() path = %v, want /bin/echo", err.Details["path"])
	}
}

func TestStargzError_WithMessage(t *testing.T) {
	err := ErrBlobNotFound.WithMessage("custom message")

	if err.Message != "custom message" {
		t.Errorf("WithMessage() message = %q, want 'custom message'", err.Message)
	}
}

func TestNewBlobNotFoundError(t *testing.T) {
	digest := digest.FromString("test-blob")
	err := NewBlobNotFoundError(digest)

	stargzErr, ok := err.(*StargzError)
	if !ok {
		t.Fatal("NewBlobNotFoundError() should return *StargzError")
	}

	if stargzErr.Code != "BLOB_NOT_FOUND" {
		t.Errorf("Code = %q, want BLOB_NOT_FOUND", stargzErr.Code)
	}

	if stargzErr.Details["blobDigest"] != digest.String() {
		t.Errorf("blobDigest detail = %v, want %v", stargzErr.Details["blobDigest"], digest.String())
	}
}

func TestNewFileNotFoundError(t *testing.T) {
	digest := digest.FromString("test-blob")
	err := NewFileNotFoundError("/bin/echo", digest)

	stargzErr, ok := err.(*StargzError)
	if !ok {
		t.Fatal("NewFileNotFoundError() should return *StargzError")
	}

	if stargzErr.Details["path"] != "/bin/echo" {
		t.Errorf("path detail = %v, want /bin/echo", stargzErr.Details["path"])
	}

	if stargzErr.Details["blobDigest"] != digest.String() {
		t.Errorf("blobDigest detail = %v, want %v", stargzErr.Details["blobDigest"], digest.String())
	}
}

func TestNewFileNotFoundError_WithoutDigest(t *testing.T) {
	err := NewFileNotFoundError("/bin/echo", "")

	stargzErr, ok := err.(*StargzError)
	if !ok {
		t.Fatal("NewFileNotFoundError() should return *StargzError")
	}

	if stargzErr.Details["path"] != "/bin/echo" {
		t.Errorf("path detail = %v, want /bin/echo", stargzErr.Details["path"])
	}

	if _, exists := stargzErr.Details["blobDigest"]; exists {
		t.Error("blobDigest should not be in details when empty")
	}
}

func TestNewManifestFetchError(t *testing.T) {
	cause := errors.New("network error")
	err := NewManifestFetchError("ghcr.io/test/image:latest", cause)

	stargzErr, ok := err.(*StargzError)
	if !ok {
		t.Fatal("NewManifestFetchError() should return *StargzError")
	}

	if stargzErr.Code != "MANIFEST_FETCH_FAILED" {
		t.Errorf("Code = %q, want MANIFEST_FETCH_FAILED", stargzErr.Code)
	}

	if stargzErr.Cause != cause {
		t.Errorf("Cause = %v, want %v", stargzErr.Cause, cause)
	}
}

func TestNewDownloadError(t *testing.T) {
	cause := errors.New("io error")
	err := NewDownloadError("/bin/echo", 3, cause)

	stargzErr, ok := err.(*StargzError)
	if !ok {
		t.Fatal("NewDownloadError() should return *StargzError")
	}

	if stargzErr.Details["attempts"] != 3 {
		t.Errorf("attempts detail = %v, want 3", stargzErr.Details["attempts"])
	}
}

func TestIsStargzError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "StargzError",
			err:  ErrBlobNotFound,
			want: true,
		},
		{
			name: "StargzError with cause",
			err:  ErrBlobNotFound.WithCause(errors.New("test")),
			want: true,
		},
		{
			name: "standard error",
			err:  errors.New("test"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStargzError(tt.err); got != tt.want {
				t.Errorf("IsStargzError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetErrorCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "StargzError",
			err:  ErrBlobNotFound,
			want: "BLOB_NOT_FOUND",
		},
		{
			name: "StargzError with modifications",
			err:  NewBlobNotFoundError(digest.FromString("test")),
			want: "BLOB_NOT_FOUND",
		},
		{
			name: "standard error",
			err:  errors.New("test"),
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetErrorCode(tt.err); got != tt.want {
				t.Errorf("GetErrorCode() = %q, want %q", got, tt.want)
			}
		})
	}
}
