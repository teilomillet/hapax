package errors

import (
	"errors"
)

// As is a wrapper around errors.As for better error type assertion
func As(err error, target interface{}) bool {
	return errors.As(err, target)
}
