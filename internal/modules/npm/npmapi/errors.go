package npmapi

import (
	"errors"
	"fmt"
)

// APIError is NPM's refusal of a request. It holds the HTTP status, and Code is NPM's error.code when that is a number
// from 400 to 599 and 0 otherwise. It never holds any text of the answer, because a compromised NPM can place a secret
// it was sent, such as the administrator's password or the bearer token, in that text.
type APIError struct {
	Status int
	Code   int
}

// Error implements error.
func (e *APIError) Error() string {
	if e.Code != 0 && e.Code != e.Status {
		return fmt.Sprintf("npmapi: NPM answered %d (error code %d)", e.Status, e.Code)
	}
	return fmt.Sprintf("npmapi: NPM answered %d", e.Status)
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
