// See LICENSE file in the project root for license information.

// Package mtlsexec implements the local, versioned external mTLS signer protocol.
package mtlsexec

const (
	Version          = 1
	MaxRequestBytes  = 2 << 20
	MaxResponseBytes = 256 << 10
	MaxDataBytes     = 1 << 20
)

// Request is a single JSON object written to the executable's standard input.
// Data uses JSON's base64 encoding for byte slices.
type Request struct {
	Version           int    `json:"version"`
	Operation         string `json:"operation"`
	CertificateSHA256 string `json:"certificateSHA256"`
	Algorithm         string `json:"algorithm,omitempty"`
	Input             string `json:"input,omitempty"`
	Data              []byte `json:"data,omitempty"`
	PSSSaltLength     *int   `json:"pssSaltLength,omitempty"`
}

type Capability struct {
	Algorithm string   `json:"algorithm"`
	Inputs    []string `json:"inputs"`
}

// Response contains either identity/signature data or a stable error code.
// Provider diagnostics belong on stderr and are never surfaced by the caller.
type Response struct {
	Version          int          `json:"version"`
	CertificateChain string       `json:"certificateChain,omitempty"`
	Capabilities     []Capability `json:"capabilities,omitempty"`
	Signature        []byte       `json:"signature,omitempty"`
	Error            string       `json:"error,omitempty"`
}
