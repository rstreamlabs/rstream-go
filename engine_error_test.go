// See LICENSE file in the project root for license information.

package rstream

import (
	"errors"
	"fmt"
	"testing"

	"github.com/rstreamlabs/rstream-go/pb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestEngineError(t *testing.T) {
	tests := []struct {
		name      string
		code      EngineErrorCode
		retryable bool
	}{
		{name: "invalid request", code: EngineErrorCodeInvalidRequest},
		{name: "feature unavailable", code: EngineErrorCodeFeatureNotAvailable},
		{name: "service unavailable", code: EngineErrorCodeServiceUnavailable, retryable: true},
		{name: "capacity exhausted", code: EngineErrorCodeCapacityExhausted, retryable: true},
		{name: "internal", code: EngineErrorCodeInternal, retryable: true},
		{name: "unknown", code: EngineErrorCode(12345)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &EngineError{Code: tt.code, Message: "test"}
			if err.Error() != fmt.Sprintf("engine error %d: test", tt.code) {
				t.Fatalf("Error() = %q", err.Error())
			}
			if err.Retryable() != tt.retryable {
				t.Fatalf("Retryable() = %t, want %t", err.Retryable(), tt.retryable)
			}
		})
	}
	var nilError *EngineError
	if nilError.Retryable() {
		t.Fatalf("nil EngineError must not be retryable")
	}
}

func TestNewEngineError(t *testing.T) {
	err := newEngineError(&pb.Error{Code: pb.ErrorCode_ERROR_CODE_FEATURE_NOT_AVAILABLE, Message: wrapperspb.String("disabled")})
	var engineErr *EngineError
	if !errors.As(err, &engineErr) || engineErr.Code != EngineErrorCodeFeatureNotAvailable || engineErr.Message != "disabled" {
		t.Fatalf("newEngineError() = %#v", err)
	}
	if err := newEngineError(nil); err == nil || err.Error() != "engine returned an empty error response" {
		t.Fatalf("newEngineError(nil) = %v", err)
	}
}
