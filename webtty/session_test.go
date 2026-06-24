// See LICENSE file in the project root for license information.

package webtty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

func TestIsExpectedWebTTYPeerCloseError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "eof",
			err:  io.EOF,
			want: true,
		},
		{
			name: "wrapped eof",
			err:  fmt.Errorf("read websocket: %w", io.EOF),
			want: true,
		},
		{
			name: "closed file",
			err:  os.ErrClosed,
			want: true,
		},
		{
			name: "normal websocket close",
			err:  &websocket.CloseError{Code: websocket.CloseNormalClosure},
			want: true,
		},
		{
			name: "going away websocket close",
			err:  &websocket.CloseError{Code: websocket.CloseGoingAway},
			want: true,
		},
		{
			name: "abnormal websocket close",
			err:  &websocket.CloseError{Code: websocket.CloseAbnormalClosure},
			want: true,
		},
		{
			name: "protocol error",
			err:  &websocket.CloseError{Code: websocket.CloseProtocolError},
			want: false,
		},
		{
			name: "authorization error",
			err:  errors.New("WebTTY client signing key is not authorized"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isExpectedWebTTYPeerCloseError(tt.err); got != tt.want {
				t.Fatalf("isExpectedWebTTYPeerCloseError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebTTYProtocolErrorForError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code pb.ProtocolErrorCode
		ok   bool
	}{
		{
			name: "client proof required",
			err:  errWebTTYClientProofRequired,
			code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_PROOF_REQUIRED,
			ok:   true,
		},
		{
			name: "client proof invalid",
			err:  fmt.Errorf("%w: bad signature", errWebTTYClientProofInvalid),
			code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_PROOF_INVALID,
			ok:   true,
		},
		{
			name: "wrapped client proof required",
			err:  fmt.Errorf("open rejected: %w", errWebTTYClientProofRequired),
			code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_PROOF_REQUIRED,
			ok:   true,
		},
		{
			name: "client unauthorized",
			err:  errWebTTYClientProofUnauthorized,
			code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_UNAUTHORIZED,
			ok:   true,
		},
		{
			name: "wrapped client unauthorized",
			err:  fmt.Errorf("open rejected: %w", errWebTTYClientProofUnauthorized),
			code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_UNAUTHORIZED,
			ok:   true,
		},
		{
			name: "generic error",
			err:  errors.New("generic failure"),
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := webTTYProtocolErrorForError(tt.err)
			if ok != tt.ok {
				t.Fatalf("webTTYProtocolErrorForError() ok = %v, want %v", ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if got == nil {
				t.Fatal("webTTYProtocolErrorForError() returned nil protocol error")
			}
			if got.Code != tt.code {
				t.Fatalf("protocol error code = %v, want %v", got.Code, tt.code)
			}
			if got.Msg == "" {
				t.Fatal("protocol error message is empty")
			}
		})
	}
}
