package apierr_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/labspangaea/go-lib/apierr"
)

func TestCodeErrEnum_Error(t *testing.T) {
	tests := []struct {
		code apierr.CodeErrEnum
		want string
	}{
		{apierr.CodeErrGeneral, "something went wrong"},
		{apierr.CodeErrNotFound, "not found"},
		{apierr.CodeErrBadRequest, "bad request"},
		{apierr.CodeErrValidation, "validation error"},
		{apierr.CodeErrUnauthorized, "unauthorized"},
		{apierr.CodeErrForbidden, "forbidden"},
		{apierr.CodeErrConflict, "conflict"},
		{apierr.CodeErrTooManyRequests, "rate limit exceeded. please try again later"},
		{apierr.CodeErrUnprocessable, "unprocessable entity"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.code.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCodeErrEnum_GetCodeErr(t *testing.T) {
	ce := apierr.CodeErrNotFound.GetCodeErr()
	if ce.Code != "ERR404000" {
		t.Errorf("Code = %q, want ERR404000", ce.Code)
	}
	if ce.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", ce.StatusCode, http.StatusNotFound)
	}
	if ce.Message != "not found" {
		t.Errorf("Message = %q, want not found", ce.Message)
	}
}

func TestCodeErrEnum_GetCodeErr_UnknownFallsBackToGeneral(t *testing.T) {
	unknown := apierr.CodeErrEnum(9999)
	ce := unknown.GetCodeErr()
	if ce.Code != "ERR500000" {
		t.Errorf("unknown code should fall back to General, got Code=%q", ce.Code)
	}
	if ce.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", ce.StatusCode, http.StatusInternalServerError)
	}
}

func TestCodeErrEnum_WithDetail(t *testing.T) {
	cause := fmt.Errorf("user id=42 not found in database")
	ce := apierr.CodeErrNotFound.WithDetail(cause)

	if ce.Detail != cause.Error() {
		t.Errorf("Detail = %q, want %q", ce.Detail, cause.Error())
	}
	if ce.Code != "ERR404000" {
		t.Errorf("Code changed: %q, want ERR404000", ce.Code)
	}
	if ce.Message != "not found" {
		t.Errorf("Message changed: %q, want not found", ce.Message)
	}
}

func TestCodeErr_Error(t *testing.T) {
	ce := apierr.CodeErr{Message: "custom message"}
	if ce.Error() != "custom message" {
		t.Errorf("Error() = %q, want custom message", ce.Error())
	}
}

func TestCodeErrEnum_ImplementsError(t *testing.T) {
	var err error = apierr.CodeErrNotFound
	if err.Error() != "not found" {
		t.Errorf("expected CodeErrEnum to implement error, got %q", err.Error())
	}
}

func TestCodeErr_ImplementsError(t *testing.T) {
	var err error = apierr.CodeErr{Message: "test"}
	if err.Error() != "test" {
		t.Errorf("expected CodeErr to implement error, got %q", err.Error())
	}
}

func TestAppendCodeErrMap(t *testing.T) {
	const customCode apierr.CodeErrEnum = 1000

	apierr.AppendCodeErrMap(customCode, apierr.CodeErr{
		Message:    "Account suspended",
		Code:       "ERR403100",
		StatusCode: http.StatusForbidden,
	})

	ce := customCode.GetCodeErr()
	if ce.Code != "ERR403100" {
		t.Errorf("Code = %q, want ERR403100", ce.Code)
	}
	if ce.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want %d", ce.StatusCode, http.StatusForbidden)
	}
}

func TestCodeErrEnum_ErrorsIs(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", apierr.CodeErrNotFound)
	if !errors.Is(err, apierr.CodeErrNotFound) {
		t.Error("expected errors.Is to match CodeErrNotFound")
	}
}
