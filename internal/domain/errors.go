package domain

import "fmt"

// Err builds an *Error with the given reason.
func Err(reason Reason, format string, args ...any) *Error {
	return &Error{Reason: reason, Msg: fmt.Sprintf(format, args...)}
}

// At returns a copy of the error with config path attached.
func (e *Error) At(path string) *Error {
	cp := *e
	cp.Path = path
	return &cp
}

// From returns a copy of the error with source name attached.
func (e *Error) From(source string) *Error {
	cp := *e
	cp.Source = source
	return &cp
}
