// See LICENSE file in the project root for license information.

package controlplane

import (
	"fmt"
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
