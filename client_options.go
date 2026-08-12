// See LICENSE file in the project root for license information.

package rstream

import (
	"crypto/tls"
	"errors"
)

type ClientOptions struct {
	Engine          string
	Token           string
	Transport       Dialer
	OwnTransport    bool
	TLSClientConfig *tls.Config
	NoToken         bool
	ZeroRTT         *bool
}

func NewClient(options ClientOptions) (*Client, error) {
	if options.Engine == "" {
		return nil, errors.New("engine is required")
	}
	if options.Token != "" && tlsConfigHasClientCertificate(options.TLSClientConfig) {
		return nil, errors.New("token and mTLS authentication cannot be used together")
	}
	engine := options.Engine
	transport := options.Transport
	ownTransport := options.OwnTransport
	if isNilDialer(transport) {
		transport = &AutoTransport{}
		ownTransport = true
	}
	client := &Client{
		EngineURL:       &engine,
		Transport:       transport,
		ownsTransport:   ownTransport,
		TLSClientConfig: options.TLSClientConfig,
		ZeroRTT:         options.ZeroRTT,
	}
	if options.Token != "" {
		token := options.Token
		client.Token = &token
	}
	if options.NoToken {
		client.NoToken = BoolPtr(true)
	}
	if options.Token == "" && tlsConfigHasClientCertificate(options.TLSClientConfig) {
		client.NoToken = BoolPtr(true)
	}
	return client, nil
}
