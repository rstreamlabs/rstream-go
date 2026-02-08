// See LICENSE file in the project root for license information.

package rstream

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/rstreamlabs/rstream-go/pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func FormatForwardingAddr(props TunnelProperties) (string, error) {
	if props.Host != nil {
		host := *props.Host
		switch {
		case props.Protocol != nil && *props.Protocol == ProtocolHTTP:
			return "https://" + host, nil
		case props.Protocol != nil && *props.Protocol == ProtocolTLS:
			return host + " (tls)", nil
		case props.Protocol != nil && *props.Protocol == ProtocolDTLS:
			return host + " (dtls)", nil
		case props.Protocol != nil && *props.Protocol == ProtocolQUIC:
			return host + " (quic)", nil
		default:
			return host, nil
		}
	}
	if props.Name != nil {
		return "rstrm://" + *props.Name + " (unpublished)", nil
	}
	if props.ID != nil {
		return "rstrm://" + *props.ID + " (unpublished)", nil
	}
	return "", errors.New("invalid tunnel properties: no host, name, or ID")
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
		Channel:         stringPbValueOrNil(details.Channel),
		Version:         stringPbValueOrNil(details.Version),
		Os:              stringPbValueOrNil(details.OS),
		Arch:            stringPbValueOrNil(details.Arch),
		Token:           stringPbValueOrNil(details.Token),
		ProtocolVersion: stringPbValueOrNil(details.ProtocolVersion),
	}
}

func toTunnelProperties(msg *pb.TunnelProperties) TunnelProperties {
	return TunnelProperties{
		ID:             stringPtrFromPbValue(msg.Id),
		CreationDate:   timePtrFromPbValue(msg.CreationDate),
		Name:           stringPtrFromPbValue(msg.Name),
		Type:           (*TunnelType)(stringPtrFromPbValue(msg.Type)),
		Publish:        boolPtrFromPbValue(msg.Publish),
		Protocol:       (*Protocol)(stringPtrFromPbValue(msg.Protocol)),
		Labels:         msg.Labels,
		GeoIP:          msg.Geoip,
		TrustedIPs:     msg.TrustedIps,
		Host:           stringPtrFromPbValue(msg.Host),
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
		CreationDate:   timestampPbValueOrNil(props.CreationDate),
		Name:           stringPbValueOrNil(props.Name),
		Type:           stringPbValueOrNil((*string)(props.Type)),
		Publish:        boolPbValueOrNil(props.Publish),
		Protocol:       stringPbValueOrNil((*string)(props.Protocol)),
		Labels:         props.Labels,
		Geoip:          props.GeoIP,
		TrustedIps:     props.TrustedIPs,
		Host:           stringPbValueOrNil(props.Host),
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

func getClientDetails(token *string) (*clientDetails, error) {
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
		Agent:           &Agent,
		Channel:         &Channel,
		Version:         &Version,
		OS:              &OS,
		Arch:            &Arch,
		Token:           token,
		ProtocolVersion: protocolVersion,
	}, nil
}
