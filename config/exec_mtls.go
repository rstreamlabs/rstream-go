// See LICENSE file in the project root for license information.

package config

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"reflect"
	"strings"

	"github.com/rstreamlabs/rstream-go/internal/fipsprofile"
	"github.com/rstreamlabs/rstream-go/internal/mtlsexec"
)

func loadExecMTLSConfig(storage *MTLSStorage) (*tls.Config, error) {
	provider, err := newExecMTLSProvider(storage)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, GetClientCertificate: provider.GetClientCertificate}, nil
}

func newExecMTLSProvider(storage *MTLSStorage) (*mtlsexec.Provider, error) {
	if err := fipsprofile.Unavailable("external mTLS signer"); err != nil {
		return nil, err
	}
	if storage.Exec == nil {
		return nil, errors.New("exec mTLS storage requires exec settings")
	}
	other := *storage
	other.Kind, other.CertificateSHA256, other.Exec = "", "", nil
	if !reflect.DeepEqual(other, MTLSStorage{}) {
		return nil, errors.New("exec mTLS storage cannot include another backend or certificate source")
	}
	fingerprint, err := decodeCertificateSHA256(storage.CertificateSHA256)
	if err != nil {
		return nil, err
	}
	cfg := storage.Exec
	provider, err := mtlsexec.New(mtlsexec.Options{
		Command: cfg.Command, Args: cfg.Args, Timeout: cfg.Timeout,
		MaxConcurrency: cfg.MaxConcurrency, PassEnv: cfg.PassEnv,
		CertificateSHA256: fingerprint,
	})
	if err != nil {
		return nil, err
	}
	return provider, nil
}

// CheckExternalMTLS explicitly probes a resolved exec credential without
// contacting the Engine. Ordinary resolution and context inspection never run
// the executable. A nil certificate means that another backend is selected.
func (r Resolved) CheckExternalMTLS(ctx context.Context) (*x509.Certificate, error) {
	if r.mtlsSource == nil || r.mtlsSource.Storage == nil || strings.TrimSpace(r.mtlsSource.Storage.Kind) != MTLSStorageExec {
		return nil, nil
	}
	provider, err := newExecMTLSProvider(r.mtlsSource.Storage)
	if err != nil {
		return nil, err
	}
	return provider.Check(ctx)
}
