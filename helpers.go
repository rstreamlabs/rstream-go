// See LICENSE file in the project root for license information.

package rstream

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/rstreamlabs/rstream-go/pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func FormatForwardingAddr(props TunnelProperties) (string, error) {
	if host, ok := publishedHost(props); ok {
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

func publishedHost(props TunnelProperties) (string, bool) {
	if props.Hostname != nil && strings.TrimSpace(*props.Hostname) != "" {
		host := strings.TrimSpace(*props.Hostname)
		port := uint32(443)
		if props.Port != nil && *props.Port > 0 {
			port = *props.Port
		}
		if (props.Protocol != nil && *props.Protocol == ProtocolTLS) || port != 443 {
			return net.JoinHostPort(host, strconv.FormatUint(uint64(port), 10)), true
		}
		return host, true
	}
	if props.Host != nil && strings.TrimSpace(*props.Host) != "" {
		return strings.TrimSpace(*props.Host), true
	}
	return "", false
}

func tunnelUsesUpstreamTLS(props TunnelProperties) bool {
	if props.UpstreamTLS != nil {
		return *props.UpstreamTLS
	}
	return props.HTTPUseTLS != nil && *props.HTTPUseTLS
}

func FormatForwardedHostPort(host, port string, props TunnelProperties) (string, error) {
	isHTTP := props.Protocol != nil && *props.Protocol == ProtocolHTTP
	isH3 := props.HTTPVersion != nil && *props.HTTPVersion == HTTP3
	useHTTPS := isHTTP && (isH3 || tunnelUsesUpstreamTLS(props))
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
			} else if props.UpstreamTLS != nil && *props.UpstreamTLS {
				b.WriteString(" (tls)")
			} else {
				b.WriteString(" (tcp)")
			}
		case ProtocolDTLS:
			if props.UpstreamTLS != nil && *props.UpstreamTLS {
				b.WriteString(" (dtls)")
			} else {
				b.WriteString(" (udp)")
			}
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

func toClientDetailsPb(details *ClientDetails) *pb.ClientDetails {
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
		ID:            stringPtrFromPbValue(msg.Id),
		CreationDate:  timePtrFromPbValue(msg.CreationDate),
		Name:          stringPtrFromPbValue(msg.Name),
		Type:          (*TunnelType)(stringPtrFromPbValue(msg.Type)),
		Publish:       boolPtrFromPbValue(msg.Publish),
		Protocol:      (*Protocol)(stringPtrFromPbValue(msg.Protocol)),
		Labels:        msg.Labels,
		GeoIP:         msg.Geoip,
		TrustedIPs:    msg.TrustedIps,
		Host:          stringPtrFromPbValue(msg.Host),
		Hostname:      stringPtrFromPbValue(msg.Hostname),
		Port:          uint32PtrFromPbValue(msg.Port),
		TLSMode:       (*TLSMode)(stringPtrFromPbValue(msg.TlsMode)),
		TLSALPNs:      msg.TlsAlpns,
		TLSMinVersion: stringPtrFromPbValue(msg.TlsMinVersion),
		TLSCiphers:    msg.TlsCiphers,
		MTLSAuth:      boolPtrFromPbValue(msg.MtlsAuth),
		HTTPVersion:   (*HTTPVersion)(stringPtrFromPbValue(msg.HttpVersion)),
		HTTPUseTLS:    boolPtrFromPbValue(msg.HttpUseTls),
		UpstreamTLS:   boolPtrFromPbValue(msg.UpstreamTls),
		TokenAuth:     boolPtrFromPbValue(msg.TokenAuth),
		RstreamAuth:   boolPtrFromPbValue(msg.RstreamAuth),
		ChallengeMode: boolPtrFromPbValue(msg.ChallengeMode),
	}
}

func toTunnelPropertiesPb(props TunnelProperties) *pb.TunnelProperties {
	return &pb.TunnelProperties{
		Id:            stringPbValueOrNil(props.ID),
		CreationDate:  timestampPbValueOrNil(props.CreationDate),
		Name:          stringPbValueOrNil(props.Name),
		Type:          stringPbValueOrNil((*string)(props.Type)),
		Publish:       boolPbValueOrNil(props.Publish),
		Protocol:      stringPbValueOrNil((*string)(props.Protocol)),
		Labels:        props.Labels,
		Geoip:         props.GeoIP,
		TrustedIps:    props.TrustedIPs,
		Host:          stringPbValueOrNil(props.Host),
		Hostname:      stringPbValueOrNil(props.Hostname),
		Port:          uint32PbValueOrNil(props.Port),
		TlsMode:       stringPbValueOrNil((*string)(props.TLSMode)),
		TlsAlpns:      props.TLSALPNs,
		TlsMinVersion: stringPbValueOrNil(props.TLSMinVersion),
		TlsCiphers:    props.TLSCiphers,
		MtlsAuth:      boolPbValueOrNil(props.MTLSAuth),
		HttpVersion:   stringPbValueOrNil((*string)(props.HTTPVersion)),
		HttpUseTls:    boolPbValueOrNil(props.HTTPUseTLS),
		UpstreamTls:   boolPbValueOrNil(props.UpstreamTLS),
		TokenAuth:     boolPbValueOrNil(props.TokenAuth),
		RstreamAuth:   boolPbValueOrNil(props.RstreamAuth),
		ChallengeMode: boolPbValueOrNil(props.ChallengeMode),
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

func getClientDetails(token *string) (*ClientDetails, error) {
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
	compiletime_identity := CompiletimeIdentity()
	var osValue *string
	if compiletime_identity.OS != "" {
		value := compiletime_identity.OS
		osValue = &value
	}
	var archValue *string
	if compiletime_identity.Arch != "" {
		value := compiletime_identity.Arch
		archValue = &value
	}
	return &ClientDetails{
		Agent:           &Agent,
		Channel:         &Channel,
		Version:         &Version,
		OS:              osValue,
		Arch:            archValue,
		Token:           token,
		ProtocolVersion: protocolVersion,
	}, nil
}
