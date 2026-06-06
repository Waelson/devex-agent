package errors

import (
	stderrors "errors"
	"fmt"
)

// OperationalError is a structured error for agent operational failures.
// It carries a stable ErrorCode for machine processing, an optional Operation
// label for context, and a Retryable flag to guide retry logic.
type OperationalError struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Operation string    `json:"operation,omitempty"`
	Retryable bool      `json:"retryable"`
	Cause     error     `json:"-"`
}

func (e *OperationalError) Error() string {
	if e.Operation != "" {
		return fmt.Sprintf("[%s] %s: %s", e.Code, e.Operation, e.Message)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *OperationalError) Unwrap() error {
	return e.Cause
}

// New creates a non-retryable OperationalError.
func New(code ErrorCode, message string) *OperationalError {
	return &OperationalError{Code: code, Message: message}
}

// Newf creates a non-retryable OperationalError with a formatted message.
func Newf(code ErrorCode, format string, args ...any) *OperationalError {
	return &OperationalError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// NewRetryable creates a retryable OperationalError.
func NewRetryable(code ErrorCode, message string) *OperationalError {
	return &OperationalError{Code: code, Message: message, Retryable: true}
}

// Wrap wraps a cause error with an OperationalError, preserving operation context.
func Wrap(code ErrorCode, op string, cause error) *OperationalError {
	return &OperationalError{
		Code:      code,
		Message:   cause.Error(),
		Operation: op,
		Cause:     cause,
	}
}

// WrapRetryable wraps a cause error as a retryable OperationalError.
func WrapRetryable(code ErrorCode, op string, cause error) *OperationalError {
	return &OperationalError{
		Code:      code,
		Message:   cause.Error(),
		Operation: op,
		Retryable: true,
		Cause:     cause,
	}
}

// IsRetryable reports whether err is an OperationalError marked as retryable.
func IsRetryable(err error) bool {
	var opErr *OperationalError
	if stderrors.As(err, &opErr) {
		return opErr.Retryable
	}
	return false
}

// CodeOf returns the ErrorCode of the first OperationalError in the chain,
// or an empty string if err contains no OperationalError.
func CodeOf(err error) ErrorCode {
	var opErr *OperationalError
	if stderrors.As(err, &opErr) {
		return opErr.Code
	}
	return ""
}

// Is reports whether any error in err's chain matches target.
// Delegates to the standard library.
func Is(err, target error) bool {
	return stderrors.Is(err, target)
}

// As finds the first error in err's chain that matches target.
// Delegates to the standard library.
func As(err error, target any) bool {
	return stderrors.As(err, target)
}
