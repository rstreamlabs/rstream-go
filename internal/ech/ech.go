// See LICENSE file in the project root for license information.

package ech

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

const (
	positiveCacheTTL = 10 * time.Minute
	negativeCacheTTL = time.Minute
	maxLookupDepth   = 8
)

type ResolverOptions struct {
	DNSOverride   string
	DNSOverTLS    bool
	DNSServerName string
	DNSSECEnabled bool
	ForceIPv4     bool
	ForceIPv6     bool
}

type Target struct {
	Address    string
	ServerName string
	NextProtos []string
}

type cacheEntry struct {
	configList []byte
	expiresAt  time.Time
}

type Resolver struct {
	mu    sync.Mutex
	cache map[string]cacheEntry
}

type discoveryQuery struct {
	name          string
	queryType     uint16
	supportedALPN []string
}

type dnsResponse struct {
	answer []dns.RR
}

var lookupDNSResponse = exchangeDNSQuery

func NewResolver() *Resolver {
	return &Resolver{cache: make(map[string]cacheEntry)}
}

func (r *Resolver) LookupConfigList(ctx context.Context, target Target, opts ResolverOptions) ([]byte, error) {
	key := cacheKey(target, opts)
	now := time.Now()
	r.mu.Lock()
	if entry, ok := r.cache[key]; ok {
		if now.Before(entry.expiresAt) {
			out := append([]byte(nil), entry.configList...)
			r.mu.Unlock()
			return out, nil
		}
		delete(r.cache, key)
	}
	r.mu.Unlock()
	configList, err := lookupConfigList(ctx, target, opts)
	if err != nil {
		return nil, err
	}
	ttl := positiveCacheTTL
	if len(configList) == 0 {
		ttl = negativeCacheTTL
	}
	r.mu.Lock()
	r.cache[key] = cacheEntry{configList: append([]byte(nil), configList...), expiresAt: now.Add(ttl)}
	r.mu.Unlock()
	return append([]byte(nil), configList...), nil
}

func (r *Resolver) RememberConfigList(target Target, opts ResolverOptions, configList []byte) error {
	if len(configList) == 0 {
		return nil
	}
	if _, err := parseConfigList(configList); err != nil {
		return err
	}
	r.mu.Lock()
	r.cache[cacheKey(target, opts)] = cacheEntry{configList: append([]byte(nil), configList...), expiresAt: time.Now().Add(positiveCacheTTL)}
	r.mu.Unlock()
	return nil
}

func LookupHost(ctx context.Context, host string, opts ResolverOptions) ([]string, error) {
	currentName := dns.Fqdn(normalizeDNSName(host))
	if currentName == "." {
		return nil, nil
	}
	for depth := 0; depth < maxLookupDepth; depth++ {
		addrs, alias, err := lookupHostOnce(ctx, currentName, opts)
		if err != nil {
			return nil, err
		}
		if len(addrs) > 0 {
			return addrs, nil
		}
		if alias == "" {
			return nil, nil
		}
		currentName = dns.Fqdn(alias)
	}
	return nil, nil
}

func lookupConfigList(ctx context.Context, target Target, opts ResolverOptions) ([]byte, error) {
	query, ok := discoveryQueryForTarget(target)
	if !ok {
		return nil, nil
	}
	currentName := dns.Fqdn(query.name)
	for depth := 0; depth < maxLookupDepth; depth++ {
		response, err := lookupDNSResponse(ctx, currentName, query.queryType, opts)
		if err != nil {
			return nil, err
		}
		if len(response.answer) == 0 {
			return nil, nil
		}
		configList, ok := selectConfigList(response.answer, currentName, query.queryType, query.supportedALPN)
		if ok {
			return configList, nil
		}
		nextName, ok := nextAliasTarget(response.answer, currentName, query.queryType)
		if !ok {
			return nil, nil
		}
		currentName = dns.Fqdn(nextName)
	}
	return nil, nil
}

func lookupHostOnce(ctx context.Context, qname string, opts ResolverOptions) ([]string, string, error) {
	types := []uint16{dns.TypeA, dns.TypeAAAA}
	if opts.ForceIPv4 {
		types = []uint16{dns.TypeA}
	} else if opts.ForceIPv6 {
		types = []uint16{dns.TypeAAAA}
	}
	addrs := make([]string, 0, 2)
	for _, queryType := range types {
		response, err := lookupDNSResponse(ctx, qname, queryType, opts)
		if err != nil {
			return nil, "", err
		}
		moreAddrs, alias := selectAddresses(response.answer, qname, queryType)
		addrs = append(addrs, moreAddrs...)
		if len(addrs) == 0 && alias != "" {
			return nil, alias, nil
		}
	}
	return addrs, "", nil
}

func discoveryQueryForTarget(target Target) (discoveryQuery, bool) {
	host := normalizeDNSName(target.ServerName)
	if host == "" {
		host = normalizeDNSName(hostNoPort(target.Address))
	}
	if host == "" || net.ParseIP(host) != nil {
		return discoveryQuery{}, false
	}
	port := addressPort(target.Address)
	if port == "" {
		port = "443"
	}
	if supportsRstreamALPN(target.NextProtos) {
		clusterHost, ok := clusterHostForServerName(host)
		if !ok {
			return discoveryQuery{}, false
		}
		return discoveryQuery{
			name:          "_" + port + "._rstream." + clusterHost,
			queryType:     dns.TypeSVCB,
			supportedALPN: []string{"rstrm/1"},
		}, true
	}
	if supportsHTTPSALPN(target.NextProtos) {
		name := host
		if port != "443" {
			name = "_" + port + "._https." + host
		}
		return discoveryQuery{
			name:          name,
			queryType:     dns.TypeHTTPS,
			supportedALPN: cloneStrings(target.NextProtos),
		}, true
	}
	return discoveryQuery{}, false
}

func exchangeDNSQuery(ctx context.Context, qname string, queryType uint16, opts ResolverOptions) (dnsResponse, error) {
	if opts.DNSSECEnabled && !opts.DNSOverTLS {
		return dnsResponse{}, fmt.Errorf("DNSSEC validation requires DNS over TLS with a verified resolver")
	}
	nameservers, err := nameserversForQuery(ctx, qname, opts)
	if err != nil {
		return dnsResponse{}, err
	}
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(qname), queryType)
	msg.SetEdns0(1232, opts.DNSSECEnabled)
	msg.RecursionDesired = true
	client, err := newDNSClient(opts, false)
	if err != nil {
		return dnsResponse{}, err
	}
	tcpClient, err := newDNSClient(opts, true)
	if err != nil {
		return dnsResponse{}, err
	}
	var lastErr error
	for _, nameserver := range nameservers {
		response, _, err := client.ExchangeContext(ctx, msg, ensurePort(nameserver, defaultResolverPort(opts)))
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return dnsResponse{}, ctxErr
			}
			lastErr = err
			continue
		}
		if response == nil {
			continue
		}
		if response.Truncated {
			response, _, err = tcpClient.ExchangeContext(ctx, msg, ensurePort(nameserver, defaultResolverPort(opts)))
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return dnsResponse{}, ctxErr
				}
				lastErr = err
				continue
			}
			if response == nil {
				continue
			}
		}
		switch response.Rcode {
		case dns.RcodeSuccess:
			if opts.DNSSECEnabled && !response.AuthenticatedData {
				lastErr = fmt.Errorf("DNSSEC validation required but resolver did not authenticate %s", qname)
				continue
			}
			return dnsResponse{answer: response.Answer}, nil
		case dns.RcodeNameError:
			if opts.DNSSECEnabled && !response.AuthenticatedData {
				lastErr = fmt.Errorf("DNSSEC validation required but resolver did not authenticate denial for %s", qname)
				continue
			}
			return dnsResponse{}, nil
		default:
			lastErr = fmt.Errorf("DNS query failed with rcode %s", dns.RcodeToString[response.Rcode])
		}
	}
	return dnsResponse{}, lastErr
}

func selectConfigList(records []dns.RR, currentName string, queryType uint16, supportedALPN []string) ([]byte, bool) {
	wantName := strings.ToLower(dns.Fqdn(currentName))
	for _, record := range records {
		if strings.ToLower(record.Header().Name) != wantName {
			continue
		}
		switch rr := record.(type) {
		case *dns.SVCB:
			if queryType != dns.TypeSVCB || rr.Priority == 0 {
				continue
			}
			configList, ok := configListFromRecord(rr.Value, supportedALPN)
			if ok {
				return configList, true
			}
		case *dns.HTTPS:
			if queryType != dns.TypeHTTPS || rr.Priority == 0 {
				continue
			}
			configList, ok := configListFromRecord(rr.Value, supportedALPN)
			if ok {
				return configList, true
			}
		}
	}
	return nil, false
}

func nextAliasTarget(records []dns.RR, currentName string, queryType uint16) (string, bool) {
	wantName := strings.ToLower(dns.Fqdn(currentName))
	for _, record := range records {
		if strings.ToLower(record.Header().Name) != wantName {
			continue
		}
		switch rr := record.(type) {
		case *dns.SVCB:
			if queryType == dns.TypeSVCB && rr.Priority == 0 && rr.Target != "." {
				return rr.Target, true
			}
		case *dns.HTTPS:
			if queryType == dns.TypeHTTPS && rr.Priority == 0 && rr.Target != "." {
				return rr.Target, true
			}
		case *dns.CNAME:
			return rr.Target, true
		}
	}
	return "", false
}

func selectAddresses(records []dns.RR, currentName string, queryType uint16) ([]string, string) {
	wantName := strings.ToLower(dns.Fqdn(currentName))
	addrs := make([]string, 0, len(records))
	for _, record := range records {
		if strings.ToLower(record.Header().Name) != wantName {
			continue
		}
		switch rr := record.(type) {
		case *dns.A:
			if queryType == dns.TypeA {
				addrs = append(addrs, rr.A.String())
			}
		case *dns.AAAA:
			if queryType == dns.TypeAAAA {
				addrs = append(addrs, rr.AAAA.String())
			}
		}
	}
	if len(addrs) > 0 {
		return addrs, ""
	}
	for _, record := range records {
		if strings.ToLower(record.Header().Name) != wantName {
			continue
		}
		if rr, ok := record.(*dns.CNAME); ok {
			return nil, rr.Target
		}
	}
	return nil, ""
}

func configListFromRecord(values []dns.SVCBKeyValue, supportedALPN []string) ([]byte, bool) {
	for _, value := range values {
		alpnValue, ok := value.(*dns.SVCBAlpn)
		if !ok {
			continue
		}
		if len(supportedALPN) > 0 && !hasSupportedALPN(alpnValue.Alpn, supportedALPN) {
			return nil, false
		}
	}
	for _, value := range values {
		echValue, ok := value.(*dns.SVCBECHConfig)
		if !ok || len(echValue.ECH) == 0 {
			continue
		}
		if _, err := parseConfigList(echValue.ECH); err != nil {
			return nil, false
		}
		return append([]byte(nil), echValue.ECH...), true
	}
	return nil, false
}

func hasSupportedALPN(recordALPN, supportedALPN []string) bool {
	for _, record := range recordALPN {
		for _, supported := range supportedALPN {
			if record == supported {
				return true
			}
		}
	}
	return false
}

type parsedConfig struct {
	PublicName string
}

func parseConfigList(configList []byte) ([]parsedConfig, error) {
	if len(configList) < 2 {
		return nil, fmt.Errorf("ECH config list is too short")
	}
	totalLength := int(binary.BigEndian.Uint16(configList[:2]))
	if totalLength != len(configList)-2 {
		return nil, fmt.Errorf("ECH config list length mismatch")
	}
	configs := make([]parsedConfig, 0, 1)
	offset := 2
	for offset < len(configList) {
		if len(configList[offset:]) < 4 {
			return nil, fmt.Errorf("ECH config is truncated")
		}
		length := int(binary.BigEndian.Uint16(configList[offset+2 : offset+4]))
		end := offset + 4 + length
		if end > len(configList) {
			return nil, fmt.Errorf("ECH config length mismatch")
		}
		publicName, err := parseConfigPublicName(configList[offset:end])
		if err != nil {
			return nil, err
		}
		configs = append(configs, parsedConfig{PublicName: publicName})
		offset = end
	}
	if len(configs) == 0 {
		return nil, fmt.Errorf("ECH config list is empty")
	}
	return configs, nil
}

func parseConfigPublicName(raw []byte) (string, error) {
	if len(raw) < 10 {
		return "", fmt.Errorf("ECH config is too short")
	}
	offset := 4
	offset++
	offset += 2
	publicKey, next, err := readUint16Bytes(raw, offset)
	if err != nil {
		return "", fmt.Errorf("invalid ECH public key: %w", err)
	}
	if len(publicKey) == 0 {
		return "", fmt.Errorf("ECH public key is empty")
	}
	offset = next
	if _, next, err = readUint16Bytes(raw, offset); err != nil {
		return "", fmt.Errorf("invalid ECH cipher suites: %w", err)
	}
	offset = next
	if len(raw) < offset+1 {
		return "", fmt.Errorf("missing ECH maximum name length")
	}
	offset++
	publicName, next, err := readUint8Bytes(raw, offset)
	if err != nil {
		return "", fmt.Errorf("invalid ECH public name: %w", err)
	}
	offset = next
	if len(publicName) == 0 {
		return "", fmt.Errorf("ECH public name is empty")
	}
	if _, next, err = readUint16Bytes(raw, offset); err != nil {
		return "", fmt.Errorf("invalid ECH extensions: %w", err)
	}
	if next != len(raw) {
		return "", fmt.Errorf("unexpected trailing ECH config bytes")
	}
	return string(publicName), nil
}

func readUint16Bytes(raw []byte, offset int) ([]byte, int, error) {
	if len(raw) < offset+2 {
		return nil, offset, fmt.Errorf("missing uint16 length")
	}
	length := int(binary.BigEndian.Uint16(raw[offset : offset+2]))
	offset += 2
	if len(raw) < offset+length {
		return nil, offset, fmt.Errorf("truncated bytes")
	}
	return raw[offset : offset+length], offset + length, nil
}

func readUint8Bytes(raw []byte, offset int) ([]byte, int, error) {
	if len(raw) < offset+1 {
		return nil, offset, fmt.Errorf("missing uint8 length")
	}
	length := int(raw[offset])
	offset++
	if len(raw) < offset+length {
		return nil, offset, fmt.Errorf("truncated bytes")
	}
	return raw[offset : offset+length], offset + length, nil
}

func cacheKey(target Target, opts ResolverOptions) string {
	return normalizeDNSName(target.ServerName) + "\x00" + normalizeDNSName(target.Address) + "\x00" + strings.Join(target.NextProtos, ",") + "\x00" + opts.DNSOverride + "\x00" + boolKey(opts.DNSOverTLS) + "\x00" + normalizeDNSName(opts.DNSServerName) + "\x00" + boolKey(opts.DNSSECEnabled) + "\x00" + boolKey(opts.ForceIPv4) + "\x00" + boolKey(opts.ForceIPv6)
}

func boolKey(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func dnsNetwork(opts ResolverOptions) string {
	if opts.DNSOverTLS {
		if opts.ForceIPv4 {
			return "tcp4-tls"
		}
		if opts.ForceIPv6 {
			return "tcp6-tls"
		}
		return "tcp-tls"
	}
	if opts.ForceIPv4 {
		return "udp4"
	}
	if opts.ForceIPv6 {
		return "udp6"
	}
	return "udp"
}

func tcpNetwork(opts ResolverOptions) string {
	if opts.DNSOverTLS {
		if opts.ForceIPv4 {
			return "tcp4-tls"
		}
		if opts.ForceIPv6 {
			return "tcp6-tls"
		}
		return "tcp-tls"
	}
	if opts.ForceIPv4 {
		return "tcp4"
	}
	if opts.ForceIPv6 {
		return "tcp6"
	}
	return "tcp"
}

func newDNSClient(opts ResolverOptions, forceTCP bool) (*dns.Client, error) {
	client := &dns.Client{Timeout: 2 * time.Second}
	if forceTCP {
		client.Net = tcpNetwork(opts)
	} else {
		client.Net = dnsNetwork(opts)
	}
	if opts.DNSOverTLS {
		serverName, err := dnsTLSServerName(opts)
		if err != nil {
			return nil, err
		}
		client.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: serverName,
		}
	}
	return client, nil
}

func dnsTLSServerName(opts ResolverOptions) (string, error) {
	if name := normalizeDNSName(opts.DNSServerName); name != "" {
		return name, nil
	}
	if opts.DNSOverride == "" {
		return "", fmt.Errorf("DNS over TLS requires dns.override or dns.serverName")
	}
	host := normalizeDNSName(hostNoPort(opts.DNSOverride))
	if host == "" || net.ParseIP(host) != nil {
		return "", fmt.Errorf("DNS over TLS requires dns.serverName when dns.override is an IP address")
	}
	return host, nil
}

func addressPort(value string) string {
	_, port, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return port
}

func hostNoPort(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		return host
	}
	return value
}

func normalizeDNSName(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func clusterHostForServerName(serverName string) (string, bool) {
	host := normalizeDNSName(hostNoPort(serverName))
	if host == "" || net.ParseIP(host) != nil {
		return "", false
	}
	labels := strings.Split(host, ".")
	if len(labels) >= 3 && labels[1] == "t" {
		return strings.Join(labels[2:], "."), true
	}
	if len(labels) >= 2 && looksLikeProjectEndpoint(labels[0]) {
		return strings.Join(labels[1:], "."), true
	}
	return host, true
}

func looksLikeProjectEndpoint(label string) bool {
	if len(label) != 8 {
		return false
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func ensurePort(address, defaultPort string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(address, defaultPort)
}

func supportsRstreamALPN(nextProtos []string) bool {
	for _, nextProto := range nextProtos {
		if nextProto == "rstrm/1" {
			return true
		}
	}
	return false
}

func supportsHTTPSALPN(nextProtos []string) bool {
	for _, nextProto := range nextProtos {
		if nextProto == "http/1.1" || nextProto == "h2" || nextProto == "h3" {
			return true
		}
	}
	return false
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
