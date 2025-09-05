// See LICENSE file in the project root for license information.

package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rstreamlabs/rstream-go"
	"github.com/spf13/cobra"
)

func getStringPtr(cmd *cobra.Command, name string) *string {
	f := cmd.Flags().Lookup(name)
	if f != nil && f.Changed {
		val, _ := cmd.Flags().GetString(name)
		return &val
	}
	return nil
}

func getBoolPtr(cmd *cobra.Command, name string) *bool {
	f := cmd.Flags().Lookup(name)
	if f != nil && f.Changed {
		val, _ := cmd.Flags().GetBool(name)
		return &val
	}
	return nil
}

func getInt64Ptr(cmd *cobra.Command, name string) *int64 {
	f := cmd.Flags().Lookup(name)
	if f != nil && f.Changed {
		val, _ := cmd.Flags().GetInt64(name)
		return &val
	}
	return nil
}

func getStringArrayMap(cmd *cobra.Command, name string) map[string]string {
	f := cmd.Flags().Lookup(name)
	if f != nil && f.Changed {
		arr, _ := cmd.Flags().GetStringArray(name)
		m := make(map[string]string)
		for _, kv := range arr {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				m[parts[0]] = parts[1]
			}
		}
		if len(m) > 0 {
			return m
		}
	}
	return nil
}

func getStringSlice(cmd *cobra.Command, name string) []string {
	f := cmd.Flags().Lookup(name)
	if f != nil && f.Changed {
		val, _ := cmd.Flags().GetStringSlice(name)
		return val
	}
	return nil
}

func newClientFromFlags(cmd *cobra.Command) (*rstream.Client, error) {
	configFilePathPtr := getStringPtr(cmd, "config")
	tokenPtr := getStringPtr(cmd, "token")
	noTokenPtr := getBoolPtr(cmd, "no-token")
	enginePtr := getStringPtr(cmd, "engine")
	tlsCfg, err := buildTLSConfig(
		cmd,
		"tls-cert-file",
		"tls-key-file",
		"tls-cacert-file",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build client TLS config: %w", err)
	}
	localAddrPtr := getStringPtr(cmd, "local-addr")
	networkInterfacePtr := getStringPtr(cmd, "network-interface")
	forceIPv4Ptr := getBoolPtr(cmd, "force-ipv4")
	forceIPv6Ptr := getBoolPtr(cmd, "force-ipv6")
	dnsOverridePtr := getStringPtr(cmd, "dns-override")
	mptcpPtr := getBoolPtr(cmd, "mptcp")
	proxyHttpPtr := getStringPtr(cmd, "proxy-http")
	proxyUsernamePtr := getStringPtr(cmd, "proxy-username")
	proxyPasswordPtr := getStringPtr(cmd, "proxy-password")
	proxyHTTPHeaders := getStringArrayMap(cmd, "proxy-http-headers")
	tlsProxyConfig, err := buildTLSConfig(
		cmd,
		"proxy-tls-cert-file",
		"proxy-tls-key-file",
		"proxy-tls-cacert-file",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build proxy TLS config: %w", err)
	}
	client := &rstream.Client{
		ConfigFilePath: configFilePathPtr,
		Transport: &rstream.Transport{
			LocalAddr:        localAddrPtr,
			NetworkInterface: networkInterfacePtr,
			ForceIPv4:        forceIPv4Ptr,
			ForceIPv6:        forceIPv6Ptr,
			DNSOverride:      dnsOverridePtr,
			MPTCPEnabled:     mptcpPtr,
			ProxyHTTP:        proxyHttpPtr,
			ProxyUsername:    proxyUsernamePtr,
			ProxyPassword:    proxyPasswordPtr,
			ProxyHTTPHeaders: proxyHTTPHeaders,
			TLSProxyConfig:   tlsProxyConfig,
		},
		TLSClientConfig: tlsCfg,
		EngineURL:       enginePtr,
		Token:           tokenPtr,
		NoToken:         noTokenPtr,
	}
	return client, nil
}

func newTunnelPropertiesFromFlags(cmd *cobra.Command) (*rstream.TunnelProperties, error) {
	namePtr := getStringPtr(cmd, "name")
	bytestreamPtr := getBoolPtr(cmd, "bytestream")
	datagramPtr := getBoolPtr(cmd, "datagram")
	var typePtr *rstream.TunnelType
	if bytestreamPtr != nil && *bytestreamPtr {
		t := rstream.TunnelTypeBytestream
		typePtr = &t
	} else if datagramPtr != nil && *datagramPtr {
		t := rstream.TunnelTypeDatagram
		typePtr = &t
	}
	publishPtr := getBoolPtr(cmd, "publish")
	noPublishPtr := getBoolPtr(cmd, "no-publish")
	var publishFinalPtr *bool
	switch {
	case publishPtr != nil && *publishPtr:
		publishFinalPtr = rstream.BoolPtr(true)
	case noPublishPtr != nil && *noPublishPtr:
		publishFinalPtr = rstream.BoolPtr(false)
	default:
	}
	tlsPtr := getBoolPtr(cmd, "tls")
	dtlsPtr := getBoolPtr(cmd, "dtls")
	quicPtr := getBoolPtr(cmd, "quic")
	httpPtr := getBoolPtr(cmd, "http")
	var protocol *rstream.Protocol
	if tlsPtr != nil && *tlsPtr {
		p := rstream.ProtocolTLS
		protocol = &p
	} else if dtlsPtr != nil && *dtlsPtr {
		p := rstream.ProtocolDTLS
		protocol = &p
	} else if quicPtr != nil && *quicPtr {
		p := rstream.ProtocolQUIC
		protocol = &p
	} else if httpPtr != nil && *httpPtr {
		p := rstream.ProtocolHTTP
		protocol = &p
	}
	labels := getStringArrayMap(cmd, "label")
	geoipSlice := getStringSlice(cmd, "geoip")
	trustedIPsSlice := getStringSlice(cmd, "trusted-ips")
	domainPtr := getStringPtr(cmd, "domain")
	var tlsModePtr *rstream.TLSMode
	if cmd.Flags().Lookup("tls-mode").Changed {
		val, _ := cmd.Flags().GetString("tls-mode")
		switch val {
		case "terminated":
			tmp := rstream.TLSModeTerminated
			tlsModePtr = &tmp
		case "passthrough":
			tmp := rstream.TLSModePassthrough
			tlsModePtr = &tmp
		}
	}
	var tlsALPNSlice []string
	if cmd.Flags().Lookup("tls-alpn").Changed {
		v, _ := cmd.Flags().GetString("tls-alpn")
		if v != "" {
			tlsALPNSlice = strings.Split(v, ",")
		}
		if len(tlsALPNSlice) == 0 {
			tlsALPNSlice = nil
		}
	}
	tlsMinVersionPtr := getStringPtr(cmd, "tls-min-version")
	tlsCipherIDs := getStringSlice(cmd, "tls-ciphers")
	mtlsPtr := getBoolPtr(cmd, "mtls")
	mtlsCACertPtr := getStringPtr(cmd, "mtls-cacert-file")
	var httpVersionPtr *rstream.HTTPVersion
	if cmd.Flags().Lookup("http-version").Changed {
		val, _ := cmd.Flags().GetString("http-version")
		switch val {
		case string(rstream.HTTP1_1):
			tmp := rstream.HTTP1_1
			httpVersionPtr = &tmp
		case "h2c":
			tmp := rstream.HTTP2
			httpVersionPtr = &tmp
		}
	}
	httpUseTLSPtr := getBoolPtr(cmd, "http-use-tls")
	tokenAuthPtr := getBoolPtr(cmd, "token-auth")
	ssoPtr := getBoolPtr(cmd, "sso")
	var ssoProviders []string
	if cmd.Flags().Lookup("sso-provider").Changed {
		val, _ := cmd.Flags().GetString("sso-provider")
		if val != "" {
			ssoProviders = strings.Split(val, ",")
		}
		if len(ssoProviders) == 0 {
			ssoProviders = nil
		}
	}
	emailWhitelist := getStringSlice(cmd, "email-whitelist")
	emailBlacklist := getStringSlice(cmd, "email-blacklist")
	challengePtr := getBoolPtr(cmd, "challenge")
	tunnelProperties := &rstream.TunnelProperties{
		Name:           namePtr,
		Type:           typePtr,
		Publish:        publishFinalPtr,
		Protocol:       protocol,
		Labels:         labels,
		GeoIP:          geoipSlice,
		TrustedIPs:     trustedIPsSlice,
		Domain:         domainPtr,
		TLSMode:        tlsModePtr,
		TLSALPNs:       tlsALPNSlice,
		TLSMinVersion:  tlsMinVersionPtr,
		TLSCiphers:     tlsCipherIDs,
		MTLS:           mtlsPtr,
		MTLSCACertPEM:  mtlsCACertPtr,
		HTTPVersion:    httpVersionPtr,
		HTTPUseTLS:     httpUseTLSPtr,
		TokenAuth:      tokenAuthPtr,
		SSO:            ssoPtr,
		SSOProviders:   ssoProviders,
		EmailWhitelist: emailWhitelist,
		EmailBlacklist: emailBlacklist,
		Challenge:      challengePtr,
	}
	return tunnelProperties, nil
}

func parseTLSCipher(cipherName string) (uint16, error) {
	if strings.HasPrefix(cipherName, "0x") {
		val, err := strconv.ParseUint(strings.TrimPrefix(cipherName, "0x"), 16, 16)
		if err != nil {
			return 0, err
		}
		return uint16(val), nil
	}
	val, err := strconv.ParseUint(cipherName, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("unable to parse cipher: %q", cipherName)
	}
	return uint16(val), nil
}

func loadRootCAs(caPem []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(caPem); !ok {
		return nil, fmt.Errorf("unable to append CA certs")
	}
	return pool, nil
}

func buildTLSConfig(cmd *cobra.Command, certFlag, keyFlag, caFlag string) (*tls.Config, error) {
	certFile := getStringPtr(cmd, certFlag)
	keyFile := getStringPtr(cmd, keyFlag)
	caFile := getStringPtr(cmd, caFlag)
	if (certFile == nil || *certFile == "") &&
		(keyFile == nil || *keyFile == "") &&
		(caFile == nil || *caFile == "") {
		return nil, nil
	}
	cfg := &tls.Config{}
	if certFile != nil && *certFile != "" && keyFile != nil && *keyFile != "" {
		certPEM, err := os.ReadFile(*certFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read TLS cert file %q: %w", *certFile, err)
		}
		keyPEM, err := os.ReadFile(*keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read TLS key file %q: %w", *keyFile, err)
		}
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("failed to parse TLS cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	if caFile != nil && *caFile != "" {
		caPEM, err := os.ReadFile(*caFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file %q: %w", *caFile, err)
		}
		pool, err := loadRootCAs(caPEM)
		if err != nil {
			return nil, fmt.Errorf("failed to load CA certs: %w", err)
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}
