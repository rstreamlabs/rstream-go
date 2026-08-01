// See LICENSE file in the project root for license information.

package controlplane

import (
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/net/http/httpguts"
)

var reservedHeaders = map[string]struct{}{
	"Authorization":       {},
	"Connection":          {},
	"Content-Length":      {},
	"Cookie":              {},
	"Forwarded":           {},
	"Host":                {},
	"Keep-Alive":          {},
	"Proxy-Authorization": {},
	"Proxy-Connection":    {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

func NormalizeHeaders(headers map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if !httpguts.ValidHeaderFieldName(canonical) {
			return nil, fmt.Errorf("invalid control plane header name %q", name)
		}
		if _, reserved := reservedHeaders[canonical]; reserved || strings.HasPrefix(canonical, "X-Forwarded-") {
			return nil, fmt.Errorf("reserved control plane header %q", name)
		}
		if !httpguts.ValidHeaderFieldValue(value) {
			return nil, fmt.Errorf("invalid value for control plane header %q", name)
		}
		if _, exists := out[canonical]; exists {
			return nil, fmt.Errorf("duplicate control plane header %q", canonical)
		}
		out[canonical] = value
	}
	return out, nil
}
