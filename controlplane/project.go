// See LICENSE file in the project root for license information.

package controlplane

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

func (p Project) EngineAddress() string {
	if endpoint := strings.TrimSpace(p.Endpoint); endpoint != "" && strings.TrimSpace(p.Domain) != "" {
		port := p.EnginePort
		if port <= 0 {
			port = 443
		}
		return fmt.Sprintf("%s.%s:%d", endpoint, strings.TrimSpace(p.Domain), port)
	}
	if value := strings.TrimSpace(p.URL); value != "" {
		if strings.Contains(value, ":") {
			return value
		}
		return value + ":443"
	}
	return ""
}

func (p Project) EngineAddressForRegion(region string) (string, error) {
	requested, err := NormalizeRegion(region)
	if err != nil {
		return "", err
	}
	if requested == "" {
		if engine := p.EngineAddress(); engine != "" {
			return engine, nil
		}
		return "", errors.New("project does not define an engine endpoint")
	}
	if strings.EqualFold(strings.TrimSpace(p.Placement), "regional") {
		return "", errors.New("region selection is only available for global projects")
	}
	var matches []ProjectRegionalEndpoint
	availableSet := make(map[string]struct{}, len(p.RegionalEndpoints))
	for _, candidate := range p.RegionalEndpoints {
		candidateRegion := strings.ToLower(strings.TrimSpace(candidate.Region))
		if candidateRegion != "" {
			availableSet[candidateRegion] = struct{}{}
		}
		if candidateRegion == requested {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		available := make([]string, 0, len(availableSet))
		for candidate := range availableSet {
			available = append(available, candidate)
		}
		sort.Strings(available)
		if len(available) > 0 {
			return "", fmt.Errorf("region %q is not available for this project (available: %s)", requested, strings.Join(available, ", "))
		}
		return "", fmt.Errorf("region %q is not available for this project", requested)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("region %q is ambiguous for this project", requested)
	}
	endpoint := strings.TrimSpace(p.Endpoint)
	domain := strings.TrimSpace(matches[0].Domain)
	port := matches[0].EnginePort
	if endpoint == "" || domain == "" || port < 1 || port > 65535 {
		return "", fmt.Errorf("region %q has an invalid engine endpoint", requested)
	}
	return fmt.Sprintf("%s.%s:%d", endpoint, domain, port), nil
}

func NormalizeRegion(region string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(region))
	if value == "" || value == "auto" {
		return "", nil
	}
	if len(value) > 64 || !validRegion(value) {
		return "", errors.New("region can only contain letters, numbers, dots, underscores, or hyphens")
	}
	return value, nil
}

func validRegion(value string) bool {
	for i, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			continue
		}
		if i > 0 && i < len(value)-1 && (char == '.' || char == '_' || char == '-') {
			continue
		}
		return false
	}
	return value != ""
}
