//go:build rstream_fips

// See LICENSE file in the project root for license information.

package fipsprofile

// Retain the reviewed QUIC implementation in every FIPS-profile executable so
// Require can verify its exact module identity even before QUIC is first used.
import _ "github.com/quic-go/quic-go"
