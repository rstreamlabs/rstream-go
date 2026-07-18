// See LICENSE file in the project root for license information.

package rstream

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var stableDomainLabelRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func MaybeSetGeneratedStableDomain(props *TunnelProperties, engine string) error {
	if props == nil || props.Hostname != nil || props.Host != nil {
		return nil
	}
	if props.Publish != nil && !*props.Publish {
		return nil
	}
	if props.Protocol != nil && *props.Protocol == ProtocolTCP {
		return nil
	}
	hostname, ok, err := GenerateStableDomain(engine)
	if err != nil || !ok {
		return err
	}
	props.Hostname = &hostname
	return nil
}

func GenerateStableDomain(engine string) (string, bool, error) {
	host := engineHostname(engine)
	if host == "" {
		return "", false, nil
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", false, nil
	}
	projectEndpoint := labels[0]
	clusterDomain := strings.Join(labels[1:], ".")
	if !stableDomainLabelRe.MatchString(projectEndpoint) || !validClusterDomain(clusterDomain) {
		return "", false, nil
	}
	maxSlugLen := 63 - len(projectEndpoint) - 1
	if maxSlugLen < 9 {
		return "", false, nil
	}
	slug, err := randomStableDomainSlug()
	if err != nil {
		return "", false, err
	}
	if len(slug) > maxSlugLen {
		slug = slug[:maxSlugLen]
	}
	return slug + "-" + projectEndpoint + ".t." + clusterDomain, true, nil
}

func MaybeSetGeneratedStableHostname(props *TunnelProperties, engine string) error {
	return MaybeSetGeneratedStableDomain(props, engine)
}

func GenerateStableHostname(engine string) (string, bool, error) {
	return GenerateStableDomain(engine)
}

func engineHostname(engine string) string {
	value := strings.TrimSpace(engine)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		if u, err := url.Parse(value); err == nil {
			value = u.Host
		}
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if strings.Contains(value, ":") {
		return ""
	}
	return value
}

func validClusterDomain(domain string) bool {
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if !stableDomainLabelRe.MatchString(label) {
			return false
		}
	}
	return true
}

func randomStableDomainSlug() (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return "r" + hex.EncodeToString(buf[:]), nil
}
