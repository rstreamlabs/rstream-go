// See LICENSE file in the project root for license information.

package rstream

import (
	"errors"
	"fmt"

	"github.com/rstreamlabs/rstream-go/pb"
)

type EngineErrorCode int32

const (
	EngineErrorCodeUnspecified                 EngineErrorCode = EngineErrorCode(pb.ErrorCode_ERROR_CODE_UNSPECIFIED)
	EngineErrorCodeUnauthorized                EngineErrorCode = EngineErrorCode(pb.ErrorCode_ERROR_CODE_UNAUTHORIZED)
	EngineErrorCodeInvalidRequest              EngineErrorCode = EngineErrorCode(pb.ErrorCode_ERROR_CODE_INVALID_REQUEST)
	EngineErrorCodeProtocolVersionMissing      EngineErrorCode = EngineErrorCode(pb.ErrorCode_ERROR_CODE_PROTOCOL_VERSION_MISSING)
	EngineErrorCodeProtocolVersionInvalid      EngineErrorCode = EngineErrorCode(pb.ErrorCode_ERROR_CODE_PROTOCOL_VERSION_INVALID)
	EngineErrorCodeProtocolVersionIncompatible EngineErrorCode = EngineErrorCode(pb.ErrorCode_ERROR_CODE_PROTOCOL_VERSION_INCOMPATIBLE)
	EngineErrorCodeTunnelNotFound              EngineErrorCode = EngineErrorCode(pb.ErrorCode_ERROR_CODE_TUNNEL_NOT_FOUND)
	EngineErrorCodeInvalidStream               EngineErrorCode = EngineErrorCode(pb.ErrorCode_ERROR_CODE_INVALID_STREAM)
	EngineErrorCodeFeatureNotAvailable         EngineErrorCode = EngineErrorCode(pb.ErrorCode_ERROR_CODE_FEATURE_NOT_AVAILABLE)
	EngineErrorCodeServiceUnavailable          EngineErrorCode = EngineErrorCode(pb.ErrorCode_ERROR_CODE_SERVICE_UNAVAILABLE)
	EngineErrorCodeCapacityExhausted           EngineErrorCode = EngineErrorCode(pb.ErrorCode_ERROR_CODE_CAPACITY_EXHAUSTED)
	EngineErrorCodeInternal                    EngineErrorCode = EngineErrorCode(pb.ErrorCode_ERROR_CODE_INTERNAL)
)

type EngineError struct {
	Code    EngineErrorCode
	Message string
}

func (e *EngineError) Error() string {
	return fmt.Sprintf("engine error %d: %s", e.Code, e.Message)
}

func (e *EngineError) Retryable() bool {
	return e != nil && (e.Code == EngineErrorCodeServiceUnavailable || e.Code == EngineErrorCodeCapacityExhausted || e.Code == EngineErrorCodeInternal)
}

func newEngineError(value *pb.Error) error {
	if value == nil {
		return errors.New("engine returned an empty error response")
	}
	return &EngineError{Code: EngineErrorCode(value.Code), Message: value.Message.GetValue()}
}
