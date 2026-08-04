package errors

import "fmt"

// ErrorCategory classifies gateway errors by origin.
type ErrorCategory string

const (
	CategoryNetwork   ErrorCategory = "network"
	CategoryAuth      ErrorCategory = "auth"
	CategoryRateLimit ErrorCategory = "rate_limit"
	CategoryProvider  ErrorCategory = "provider"
	CategoryInternal  ErrorCategory = "internal"
)

// GatewayError is a structured error with category, code, and optional wrapped error.
type GatewayError struct {
	Category ErrorCategory
	Code     string
	Message  string
	Err      error
}

func (e *GatewayError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %s: %v", e.Category, e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s: %s", e.Category, e.Code, e.Message)
}

func (e *GatewayError) Unwrap() error {
	return e.Err
}

// ShouldRetry returns true for transient error categories.
func (e *GatewayError) ShouldRetry() bool {
	switch e.Category {
	case CategoryNetwork, CategoryRateLimit:
		return true
	default:
		return false
	}
}

// IsRetryable checks whether an error is a retryable GatewayError.
func IsRetryable(err error) bool {
	if ge, ok := err.(*GatewayError); ok {
		return ge.ShouldRetry()
	}
	return false
}

// New creates a GatewayError with the given parameters.
func New(category ErrorCategory, code, message string, err error) *GatewayError {
	return &GatewayError{
		Category: category,
		Code:     code,
		Message:  message,
		Err:      err,
	}
}
