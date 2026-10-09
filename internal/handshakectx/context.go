// See LICENSE file in the project root for license information.

// Package handshakectx preserves dial cancellation for blocking TLS signers.
package handshakectx

import "context"

type dialKey struct{}

// WithDial preserves the original context through quic-go's WithoutCancel.
// The QUIC connection outlives its dial, but a handshake signer must not.
func WithDial(ctx context.Context) context.Context {
	return context.WithValue(ctx, dialKey{}, ctx)
}

// ForSigner returns the cancellable dial context when the transport saved it.
func ForSigner(ctx context.Context) context.Context {
	if dial, ok := ctx.Value(dialKey{}).(context.Context); ok {
		return dial
	}
	return ctx
}
