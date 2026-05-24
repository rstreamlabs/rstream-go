// See LICENSE file in the project root for license information.

package webtty

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func NewBearerAuthHandler(next http.Handler, authToken *string, allowUnauthenticated bool) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorizeBearerRequest(r, authToken, allowUnauthenticated) {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authorizeBearerRequest(r *http.Request, authToken *string, allowUnauthenticated bool) bool {
	if allowUnauthenticated {
		return true
	}
	if authToken == nil || strings.TrimSpace(*authToken) == "" {
		return false
	}
	got := bearerToken(r.Header.Get("Authorization"))
	want := strings.TrimSpace(*authToken)
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return parts[1]
}
