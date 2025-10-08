package errors

import "fmt"

// Error types for stargz-get operations
var (
	// ErrBlobNotFound is returned when a requested blob digest is not found in the image
	ErrBlobNotFound = &StargzError{Code: "BLOB_NOT_FOUND", Message: "blob not found"}

	// ErrFileNotFound is returned when a file is not found in the image index
	ErrFileNotFound = &StargzError{Code: "FILE_NOT_FOUND", Message: "file not found"}

	// ErrManifestFetch is returned when manifest fetching fails
	ErrManifestFetch = &StargzError{Code: "MANIFEST_FETCH_FAILED", Message: "failed to fetch manifest"}

	// ErrTOCDownload is returned when TOC download fails
	ErrTOCDownload = &StargzError{Code: "TOC_DOWNLOAD_FAILED", Message: "failed to download TOC"}

	// ErrAuthFailed is returned when authentication fails
	ErrAuthFailed = &StargzError{Code: "AUTH_FAILED", Message: "authentication failed"}

	// ErrInvalidDigest is returned when a digest string is invalid
	ErrInvalidDigest = &StargzError{Code: "INVALID_DIGEST", Message: "invalid digest format"}

	// ErrDownloadFailed is returned when file download fails after all retries
	ErrDownloadFailed = &StargzError{Code: "DOWNLOAD_FAILED", Message: "download failed after retries"}
)

// StargzError represents a structured error in stargz-get operations
type StargzError struct {
	Code    string                 // Error code for programmatic handling
	Message string                 // Human-readable error message
	Cause   error                  // Underlying error, if any
	Details map[string]interface{} // Additional context
}

// Error implements the error interface
func (e *StargzError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	if len(e.Details) > 0 {
		return fmt.Sprintf("[%s] %s (details: %v)", e.Code, e.Message, e.Details)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap returns the underlying error
func (e *StargzError) Unwrap() error {
	return e.Cause
}

// WithCause adds a cause to the error
func (e *StargzError) WithCause(cause error) *StargzError {
	return &StargzError{
		Code:    e.Code,
		Message: e.Message,
		Cause:   cause,
		Details: e.Details,
	}
}

// WithDetail adds a detail key-value pair to the error
func (e *StargzError) WithDetail(key string, value interface{}) *StargzError {
	details := make(map[string]interface{})
	for k, v := range e.Details {
		details[k] = v
	}
	details[key] = value
	return &StargzError{
		Code:    e.Code,
		Message: e.Message,
		Cause:   e.Cause,
		Details: details,
	}
}

// WithMessage overrides the error message
func (e *StargzError) WithMessage(message string) *StargzError {
	return &StargzError{
		Code:    e.Code,
		Message: message,
		Cause:   e.Cause,
		Details: e.Details,
	}
}

// IsStargzError checks if an error is a StargzError
func IsStargzError(err error) bool {
	_, ok := err.(*StargzError)
	return ok
}

// GetErrorCode extracts the error code from a StargzError
func GetErrorCode(err error) string {
	if stargzErr, ok := err.(*StargzError); ok {
		return stargzErr.Code
	}
	return ""
}
