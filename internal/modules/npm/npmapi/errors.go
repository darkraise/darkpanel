package npmapi

import (
	"errors"
	"fmt"
)

// APIError is NPM's refusal of a request. Message is NPM's error.message, or the status text when the answer has none;
// it never holds any other part of the answer.
type APIError struct {
	Status  int
	Message string
}

// Error implements error.
func (e *APIError) Error() string {
	return fmt.Sprintf("npmapi: NPM answered %d: %s", e.Status, e.Message)
}

// IsNotFound reports whether err is NPM's 404.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == 404
}

// IsClientError reports whether err is a 4xx answer of NPM, which proves NPM committed nothing. A transport failure,
// a decode failure and a 5xx are not.
func IsClientError(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status >= 400 && ae.Status <= 499
}
