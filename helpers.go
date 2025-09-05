// See LICENSE file in the project root for license information.

package rstream

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rstreamlabs/rstream-go/pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func FormatForwardingAddr(props TunnelProperties) (string, error) {
	if props.Domain != nil {
		domain := *props.Domain
		switch {
		case props.Protocol != nil && *props.Protocol == ProtocolHTTP:
			return "https://" + domain, nil
		case props.Protocol != nil && *props.Protocol == ProtocolTLS:
			return domain + " (tls)", nil
		case props.Protocol != nil && *props.Protocol == ProtocolDTLS:
			return domain + " (dtls)", nil
		case props.Protocol != nil && *props.Protocol == ProtocolQUIC:
			return domain + " (quic)", nil
		default:
			return domain, nil
		}
	}
	if props.Name != nil {
		return "rstrm://" + *props.Name + " (unpublished)", nil
	}
	if props.ID != nil {
		return "rstrm://" + *props.ID + " (unpublished)", nil
	}
	return "", errors.New("invalid tunnel properties: no domain, name, or ID")
}

func FormatForwardedHostPort(host, port string, props TunnelProperties) (string, error) {
	isHTTP := props.Protocol != nil && *props.Protocol == ProtocolHTTP
	isH3 := props.HTTPVersion != nil && *props.HTTPVersion == HTTP3
	useHTTPS := isHTTP && (isH3 || (props.HTTPUseTLS != nil && *props.HTTPUseTLS))
	var b strings.Builder
	if isHTTP {
		if useHTTPS {
			b.WriteString("https://")
		} else {
			b.WriteString("http://")
		}
	}
	b.WriteString(host)
	if !((!useHTTPS && isHTTP && port == "80") || (useHTTPS && port == "443")) {
		b.WriteByte(':')
		b.WriteString(port)
	}
	if isHTTP && props.HTTPVersion != nil {
		if !useHTTPS || isH3 {
			b.WriteString(" (")
			b.WriteString(string(*props.HTTPVersion))
			b.WriteByte(')')
		}
		return b.String(), nil
	}
	if props.Protocol != nil {
		switch *props.Protocol {
		case ProtocolTLS:
			if props.TLSMode != nil && *props.TLSMode == TLSModePassthrough {
				b.WriteString(" (tls)")
			} else {
				b.WriteString(" (tcp)")
			}
		case ProtocolDTLS:
			b.WriteString(" (udp)")
		case ProtocolQUIC:
			b.WriteString(" (quic)")
		}
	}
	return b.String(), nil
}

func FormatForwardedAddr(addr net.TCPAddr, props TunnelProperties) (string, error) {
	if addr.IP == nil || len(addr.IP) == 0 {
		return "", errors.New("invalid address: no IP")
	}
	if addr.Port == 0 {
		return "", errors.New("invalid address: no Port")
	}
	return FormatForwardedHostPort(addr.IP.String(), strconv.Itoa(addr.Port), props)
}

func toClientDetailsPb(details *clientDetails) *pb.ClientDetails {
	return &pb.ClientDetails{
		Agent:           stringPbValueOrNil(details.Agent),
		Os:              stringPbValueOrNil(details.OS),
		Arch:            stringPbValueOrNil(details.Arch),
		Version:         stringPbValueOrNil(details.Version),
		Token:           stringPbValueOrNil(details.Token),
		ProtocolVersion: stringPbValueOrNil(details.ProtocolVersion),
	}
}

func toTunnelProperties(msg *pb.TunnelProperties) TunnelProperties {
	return TunnelProperties{
		ID:             stringPtrFromPbValue(msg.Id),
		Name:           stringPtrFromPbValue(msg.Name),
		CreationDate:   nil, // TODO
		Publish:        boolPtrFromPbValue(msg.Publish),
		Protocol:       (*Protocol)(stringPtrFromPbValue(msg.Protocol)),
		Labels:         msg.Labels,
		GeoIP:          msg.Geoip,
		TrustedIPs:     msg.TrustedIps,
		Domain:         stringPtrFromPbValue(msg.Domain),
		TLSMode:        (*TLSMode)(stringPtrFromPbValue(msg.TlsMode)),
		TLSALPNs:       msg.TlsAlpns,
		TLSMinVersion:  stringPtrFromPbValue(msg.TlsMinVersion),
		TLSCiphers:     msg.TlsCiphers,
		MTLS:           boolPtrFromPbValue(msg.Mtls),
		MTLSCACertPEM:  stringPtrFromPbValue(msg.MtlsCacertPem),
		HTTPVersion:    (*HTTPVersion)(stringPtrFromPbValue(msg.HttpVersion)),
		HTTPUseTLS:     boolPtrFromPbValue(msg.HttpUseTls),
		TokenAuth:      boolPtrFromPbValue(msg.TokenAuth),
		SSO:            boolPtrFromPbValue(msg.Sso),
		SSOProviders:   msg.SsoProviders,
		EmailWhitelist: msg.EmailWhitelist,
		EmailBlacklist: msg.EmailBlacklist,
		Challenge:      boolPtrFromPbValue(msg.Challenge),
	}
}

func toTunnelPropertiesPb(props TunnelProperties) *pb.TunnelProperties {
	return &pb.TunnelProperties{
		Id:             stringPbValueOrNil(props.ID),
		Name:           stringPbValueOrNil(props.Name),
		CreationDate:   nil, // TODO
		Publish:        boolPbValueOrNil(props.Publish),
		Protocol:       stringPbValueOrNil((*string)(props.Protocol)),
		Labels:         props.Labels,
		Geoip:          props.GeoIP,
		TrustedIps:     props.TrustedIPs,
		Domain:         stringPbValueOrNil(props.Domain),
		TlsMode:        stringPbValueOrNil((*string)(props.TLSMode)),
		TlsAlpns:       props.TLSALPNs,
		TlsMinVersion:  stringPbValueOrNil(props.TLSMinVersion),
		TlsCiphers:     props.TLSCiphers,
		Mtls:           boolPbValueOrNil(props.MTLS),
		MtlsCacertPem:  stringPbValueOrNil(props.MTLSCACertPEM),
		HttpVersion:    stringPbValueOrNil((*string)(props.HTTPVersion)),
		HttpUseTls:     boolPbValueOrNil(props.HTTPUseTLS),
		TokenAuth:      boolPbValueOrNil(props.TokenAuth),
		Sso:            boolPbValueOrNil(props.SSO),
		SsoProviders:   props.SSOProviders,
		EmailWhitelist: props.EmailWhitelist,
		EmailBlacklist: props.EmailBlacklist,
		Challenge:      boolPbValueOrNil(props.Challenge),
	}
}

func splitHostPort(addr string) (*string, *string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.Contains(addr, ":") {
			return nil, nil, err
		} else {
			host = addr
		}
	}
	var hostPtr, portPtr *string
	if host != "" {
		hostPtr = &host
	}
	if port != "" {
		portPtr = &port
	}
	return hostPtr, portPtr, nil
}

func getDefaultConfigFilePath() (string, error) {
	if envPath := os.Getenv("RSTREAM_DEFAULT_CONFIG_PATH"); envPath != "" {
		return envPath, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".rstream", "config.json"), nil
}

func loadTokensFromConfig(configPath string) (map[string]string, error) {
	f, err := os.Open(configPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var root struct {
		Tokens map[string]string `json:"tokens"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root.Tokens == nil {
		return nil, errors.New("no 'tokens' field in config")
	}
	return root.Tokens, nil
}

func getDefaultEngine() (string, error) {
	if val := os.Getenv("RSTREAM_DEFAULT_ENGINE"); val != "" {
		return val, nil
	}
	return "engine.rstream.io:443", nil
}

func getDefaultAuthToken(configFilePath *string, engine *string) (*string, error) {
	if envToken := os.Getenv("RSTREAM_DEFAULT_AUTHENTICATION_TOKEN"); envToken != "" {
		return &envToken, nil
	}
	if engine == nil {
		return nil, errors.New("engine URL is unset")
	}
	host, _, err := splitHostPort(*engine)
	if err != nil || host == nil {
		return nil, fmt.Errorf("failed to extract host from engine URL: %w", err)
	}
	if configFilePath == nil {
		filepath, err := getDefaultConfigFilePath()
		if err != nil {
			return nil, fmt.Errorf("failed to get rstream config file path: %w", err)
		}
		configFilePath = &filepath
	}
	tokens, err := loadTokensFromConfig(*configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load tokens from config: %w", err)
	}
	if token, ok := tokens[*host]; ok {
		return &token, nil
	}
	domain := *host
	for {
		dotPos := strings.IndexRune(domain, '.')
		if dotPos < 0 || dotPos+1 >= len(domain) {
			break
		}
		domain = domain[dotPos+1:]
		if token, ok := tokens[domain]; ok {
			return &token, nil
		}
	}
	return nil, errors.New("no token found in config for engine URL or any of its parent domains")
}

func getClientDetails(token *string) (*clientDetails, error) {
	agent := "rstream go SDK"
	var protocolVersion *string
	{
		fd := (&pb.ClientDetails{}).ProtoReflect().Descriptor().ParentFile()
		if opts, ok := fd.Options().(*descriptorpb.FileOptions); ok {
			if proto.HasExtension(opts, pb.E_ProtocolVersion) {
				if ext := proto.GetExtension(opts, pb.E_ProtocolVersion); ext != nil {
					tmp, ok := ext.(string)
					if ok {
						protocolVersion = &tmp
					}
				}
			}
		}
	}
	return &clientDetails{
		Agent:           &agent,
		OS:              &OS,
		Arch:            &Arch,
		Version:         &Version,
		Token:           token,
		ProtocolVersion: protocolVersion,
	}, nil
}
