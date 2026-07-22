// Package humaresponse provides generic HTTP response wrappers for huma v2
// handlers. JSON field names are wire-identical to httpx/response so API
// consumers see no difference whether a service uses huma or the older
// httpx/response builder.
//
// Usage — single entity:
//
//	func (h *Order) find(ctx context.Context, in *FindOrderInput) (*humares.DataOutput[*domain.Order], error) {
//	    o, err := h.svc.Find(ctx, in.ID)
//	    if err != nil { return nil, err }
//	    return humares.Data(o), nil
//	}
//
// Usage — list with pagination:
//
//	return humares.List(orders, humares.WithCursor[[]*domain.Order](&humares.CursorPagination{...})), nil
//
// ErrorBody implements huma.StatusError (structurally — no huma import needed)
// so the huma.NewError override can return it directly.
package humaresponse

import "github.com/labspangaea/go-lib/apierr"

// DataBody is the JSON envelope for single-entity responses.
// Wire-compatible with httpx/response.Response[T]:
//
//	{"status": true, "data": {...}}
type DataBody[T any] struct {
	Status  bool   `json:"status"`
	Message string `json:"message,omitempty"`
	Data    T      `json:"data,omitempty"`
}

// ListBody is the JSON envelope for list responses. Holds either cursor or
// offset pagination — wire-compatible with httpx/response.Response[T]:
//
//	{"status": true, "data": [...], "cursor_pagination": {...}}
//	{"status": true, "data": [...], "offset_pagination": {...}}
type ListBody[T any] struct {
	Status           bool              `json:"status"`
	Message          string            `json:"message,omitempty"`
	Data             T                 `json:"data,omitempty"`
	CursorPagination *CursorPagination `json:"cursor_pagination,omitempty"`
	OffsetPagination *OffsetPagination `json:"offset_pagination,omitempty"`
}

// DataOutput is the huma return type. huma serializes the Body field directly.
type DataOutput[T any] struct {
	Body DataBody[T]
}

// ListOutput is the huma return type for list endpoints. Single shape covers
// both cursor and offset pagination — only one *Pagination field is populated.
type ListOutput[T any] struct {
	Body ListBody[T]
}

// CursorPagination — wire-identical to httpx/response.CursorPagination.
type CursorPagination struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasNext    bool   `json:"has_next"`
	Limit      int    `json:"limit"`
}

// OffsetPagination — wire-identical to httpx/response.OffsetPagination.
type OffsetPagination struct {
	Total   int64 `json:"total"`
	Limit   int   `json:"limit"`
	Offset  int   `json:"offset"`
	HasNext bool  `json:"has_next"`
}

// ErrorBody is the JSON envelope for error responses — wire-identical to
// httpx/response.Response[any] when status=false. Implements huma.StatusError
// structurally (Error() string + GetStatus() int) so the huma.NewError
// override can return it directly without humaresponse importing huma.
type ErrorBody struct {
	Status     bool   `json:"status"` // always false
	Message    string `json:"message,omitempty"`
	ErrDetail  string `json:"error_detail,omitempty"`
	ErrCode    string `json:"error_code,omitempty"`
	statusCode int    // not serialized; surfaced via GetStatus()
}

// Error implements the error interface.
func (e *ErrorBody) Error() string { return e.Message }

// GetStatus implements huma.StatusError so huma writes the right HTTP status.
func (e *ErrorBody) GetStatus() int { return e.statusCode }

// NewError builds an ErrorBody from a CodeErr. Use inside the huma.NewError
// override in main():
//
//	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
//	    return humares.NewError(apierr.CodeErr{StatusCode: status, Message: msg, ...})
//	}
func NewError(c apierr.CodeErr) *ErrorBody {
	return &ErrorBody{
		Status:     false,
		Message:    c.Message,
		ErrDetail:  c.Detail,
		ErrCode:    c.Code,
		statusCode: c.StatusCode,
	}
}

// Data mirrors response.New[T]().WithData(data).
func Data[T any](data T) *DataOutput[T] {
	return &DataOutput[T]{Body: DataBody[T]{Status: true, Data: data}}
}

// ListOption sets pagination metadata on a ListBody.
type ListOption[T any] func(*ListBody[T])

// WithCursor mirrors response.New[T]().WithCursorPaging(data, p).
func WithCursor[T any](p *CursorPagination) ListOption[T] {
	return func(b *ListBody[T]) { b.CursorPagination = p }
}

// WithOffset mirrors response.New[T]().WithOffsetPaging(data, p).
func WithOffset[T any](p *OffsetPagination) ListOption[T] {
	return func(b *ListBody[T]) { b.OffsetPagination = p }
}

// List returns a ListOutput with status=true, data, and one pagination option applied.
func List[T any](data T, opt ListOption[T]) *ListOutput[T] {
	out := &ListOutput[T]{Body: ListBody[T]{Status: true, Data: data}}
	opt(&out.Body)
	return out
}
