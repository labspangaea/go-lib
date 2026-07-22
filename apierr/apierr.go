// Package apierr defines typed API error codes for structured error handling
// across service layers and HTTP responses.
//
// Both CodeErrEnum and CodeErr implement the error interface so they work
// naturally with errors.Is and errors.As — no sentinel imports needed in callers.
//
// Services extend the built-in set at init time:
//
//	func init() {
//	    apierr.AppendCodeErrMap(ErrUserSuspended, apierr.CodeErr{
//	        Message:    "Account suspended",
//	        Code:       "ERR403100",
//	        StatusCode: http.StatusForbidden,
//	    })
//	}
package apierr

import (
	"encoding/json"
	"net/http"
)

// CodeErrEnum is a typed integer identifying a category of API error.
// Define service-specific values as a continuation of this iota block using
// a separate const block starting from a high base (e.g. 1000) to avoid
// collisions with library-defined values.
type CodeErrEnum int

// CodeErr carries the full error descriptor returned to API callers.
type CodeErr struct {
	Message    string // user-facing message
	Detail     string // internal detail (omit in production responses)
	Code       string // machine-readable code, e.g. "ERR404000"
	StatusCode int    // HTTP status code
}

// Built-in error codes. All services have these; extend via AppendCodeErrMap.
const (
	CodeErrGeneral         CodeErrEnum = iota // ERR500000  500
	CodeErrNotFound                           // ERR404000  404
	CodeErrBadRequest                         // ERR400000  400
	CodeErrValidation                         // ERR400001  400
	CodeErrUnauthorized                       // ERR401000  401
	CodeErrForbidden                          // ERR403000  403
	CodeErrConflict                           // ERR409000  409
	CodeErrTooManyRequests                    // ERR429000  429
	CodeErrUnprocessable                      // ERR422000  422
)

var codeErrMap = map[CodeErrEnum]CodeErr{
	CodeErrGeneral:         {Message: "something went wrong", Code: "ERR500000", StatusCode: http.StatusInternalServerError},
	CodeErrNotFound:        {Message: "not found", Code: "ERR404000", StatusCode: http.StatusNotFound},
	CodeErrBadRequest:      {Message: "bad request", Code: "ERR400000", StatusCode: http.StatusBadRequest},
	CodeErrValidation:      {Message: "validation error", Code: "ERR400001", StatusCode: http.StatusBadRequest},
	CodeErrUnauthorized:    {Message: "unauthorized", Code: "ERR401000", StatusCode: http.StatusUnauthorized},
	CodeErrForbidden:       {Message: "forbidden", Code: "ERR403000", StatusCode: http.StatusForbidden},
	CodeErrConflict:        {Message: "conflict", Code: "ERR409000", StatusCode: http.StatusConflict},
	CodeErrTooManyRequests: {Message: "rate limit exceeded. please try again later", Code: "ERR429000", StatusCode: http.StatusTooManyRequests},
	CodeErrUnprocessable:   {Message: "unprocessable entity", Code: "ERR422000", StatusCode: http.StatusUnprocessableEntity},
}

// AppendCodeErrMap registers a service-specific error code and its descriptor.
// Not goroutine-safe — call only at init() or in main() before serving requests.
func AppendCodeErrMap(code CodeErrEnum, err CodeErr) {
	codeErrMap[code] = err
}

// Error implements the error interface. Returns the user-facing message.
func (c CodeErrEnum) Error() string { return c.GetCodeErr().Message }

// GetCodeErr looks up the CodeErr for c. Falls back to CodeErrGeneral if c is
// not registered — callers never receive a zero-value CodeErr.
func (c CodeErrEnum) GetCodeErr() CodeErr {
	if e, ok := codeErrMap[c]; ok {
		return e
	}
	return codeErrMap[CodeErrGeneral]
}

// WithDetail returns a copy of the CodeErr for c with Detail set to err.Error().
// Use this to attach an internal cause before passing to the response builder:
//
//	return apierr.CodeErrNotFound.WithDetail(err)
func (c CodeErrEnum) WithDetail(err error) CodeErr {
	e := c.GetCodeErr()
	e.Detail = err.Error()
	return e
}

// GetStatus returns the HTTP status code for c. Satisfies huma.StatusError so
// huma handlers can return a CodeErrEnum and have huma honor its status code.
func (c CodeErrEnum) GetStatus() int { return c.GetCodeErr().StatusCode }

// MarshalJSON serializes c as the canonical error envelope so a CodeErrEnum
// returned directly from a huma handler renders as
// {status:false, message, error_detail, error_code} instead of its raw int
// value. Resolves via GetCodeErr so service-specific codes inherit the same
// shape.
func (c CodeErrEnum) MarshalJSON() ([]byte, error) {
	return c.GetCodeErr().MarshalJSON()
}

// Error implements the error interface. Returns the user-facing message.
func (c CodeErr) Error() string { return c.Message }

// GetStatus returns c.StatusCode. Satisfies huma.StatusError so huma handlers
// can return a CodeErr and have huma honor its status code.
func (c CodeErr) GetStatus() int { return c.StatusCode }

// MarshalJSON serializes c as the canonical error envelope. The unexported
// shape is kept here (instead of struct tags on CodeErr) so the public field
// names — Message/Detail/Code/StatusCode — stay legible to Go callers while
// the wire format matches httpx/humaresponse.ErrorBody.
func (c CodeErr) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Status    bool   `json:"status"`
		Message   string `json:"message,omitempty"`
		ErrDetail string `json:"error_detail,omitempty"`
		ErrCode   string `json:"error_code,omitempty"`
	}{
		Status:    false,
		Message:   c.Message,
		ErrDetail: c.Detail,
		ErrCode:   c.Code,
	})
}
