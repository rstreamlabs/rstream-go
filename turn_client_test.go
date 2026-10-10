// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/controlplane"
)

const turnTestAPIResponse = `{"username":"v2:2000000000:issued:127:credential:certificate-1:k1","credential":"password","urls":["turn:relay.test:3478?transport=udp"],"ttl":600}`

func TestMTLSTURNCredentialsReuseDiscoveryWithoutControlPlane(t *testing.T) {
	for _, protocol := range []string{"http1", "http2", "http3"} {
		t.Run(protocol, func(t *testing.T) {
			f := newDiscoveryFixture(t, nil, true, protocol == "http2")
			var issued, controlPlaneCalls atomic.Int32
			controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				controlPlaneCalls.Add(1)
				http.Error(w, "unexpected", 500)
			}))
			defer controlPlane.Close()
			f.dedicated.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/turn-server/credentials" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Control-Plane") != "" || len(r.TLS.PeerCertificates) != 1 {
					t.Error("incorrect issuance request")
				}
				var params struct {
					TTLSeconds int `json:"ttlSeconds"`
				}
				if err := json.NewDecoder(r.Body).Decode(&params); err != nil || params.TTLSeconds != 600 {
					t.Error("incorrect TTL", params, err)
				}
				issued.Add(1)
				_, _ = io.WriteString(w, turnTestAPIResponse)
			})
			if protocol == "http3" {
				f.client.Transport = &QUICTransport{}
				serveAPIHTTP3(t, f.ordinary.Listener.Addr().String(), f.ordinary.TLS, f.ordinary.Config.Handler)
				serveAPIHTTP3(t, f.dedicated.Listener.Addr().String(), f.dedicated.TLS, f.dedicated.Config.Handler)
			} else {
				f.client.Transport = &Transport{}
			}
			var group sync.WaitGroup
			for range 16 {
				group.Go(func() {
					res, err := CreateTURNCredentials(t.Context(), CreateTURNCredentialsOptions{Client: f.client, APIURL: controlPlane.URL, TTL: 600 * time.Second, ControlPlaneHeaders: map[string]string{"X-Control-Plane": "private"}})
					if err != nil || res == nil || res.TTL != 600 {
						t.Errorf("issuance=%+v err=%v", res, err)
					}
				})
			}
			group.Wait()
			if issued.Load() != 16 || f.discoveries.Load() != 1 || controlPlaneCalls.Load() != 0 {
				t.Fatalf("issued=%d discovery=%d control plane=%d", issued.Load(), f.discoveries.Load(), controlPlaneCalls.Load())
			}
		})
	}
}

func TestTURNControlPlaneFallbackUsesSameIdentityAndScope(t *testing.T) {
	for _, status := range []int{200, 400, 401, 403, 404, 409, 429, 500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			engine := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer issuance-token" || r.Header.Get("X-Control-Plane") != "" {
					t.Error("identity changed or Control plane header leaked")
				}
				_, _ = io.WriteString(w, turnTestAPIResponse)
			}))
			defer engine.Close()
			controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer issuance-token" || r.Header.Get("X-Control-Plane") != "private" || r.URL.Path != "/api/projects/tunnels/resolve/127/turn-server/credentials" {
					t.Error("wrong Control plane identity or scope")
				}
				w.WriteHeader(status)
				if status == 200 {
					_, _ = io.WriteString(w, turnTestAPIResponse)
				} else {
					_, _ = io.WriteString(w, `{"error":"unavailable"}`)
				}
			}))
			defer controlPlane.Close()
			client := testAPIClient(engine, "issuance-token")
			defer client.Close()
			result, err := client.CreateTURNCredentials(t.Context(), CreateTURNCredentialsOptions{APIURL: controlPlane.URL, ProjectEndpoint: "127", ControlPlaneHeaders: map[string]string{"X-Control-Plane": "private"}})
			fallback := status == 502 || status == 503 || status == 504
			if fallback || status == 200 {
				if err != nil || result == nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("expected authoritative API denial")
			}
			want := int32(0)
			if fallback {
				want = 1
			}
			if calls.Load() != want {
				t.Fatalf("Engine requests=%d, want %d", calls.Load(), want)
			}
		})
	}
}

func TestTURNValidationAndExplicitModesDoNotContactAnotherAPI(t *testing.T) {
	f := newDiscoveryFixture(t, nil, true, false)
	for _, opts := range []CreateTURNCredentialsOptions{
		{TTL: -time.Second}, {TTL: time.Millisecond}, {TTL: time.Hour + time.Second}, {TTL: 1500 * time.Millisecond},
		{Token: "token"}, {Mode: modePtr(TURNCredentialModeAPI)}, {Mode: modePtr(TURNCredentialModePAT)},
		{ProjectEndpoint: "other"}, {ProjectID: "unbound"},
	} {
		if _, err := f.client.CreateTURNCredentials(t.Context(), opts); err == nil {
			t.Errorf("invalid options accepted: %+v", opts)
		}
	}
	if f.discoveries.Load() != 0 {
		t.Fatal("invalid input caused network discovery")
	}
	for _, response := range []TURNCredentials{
		{Username: "u", Credential: "p", URLs: []string{"turn:relay:3478"}, TTL: 0},
		{Username: "u", Credential: "p", URLs: []string{"turn:relay:3478"}, TTL: 3601},
		{Username: "u", Credential: "p", URLs: []string{"https://relay"}, TTL: 600},
	} {
		if err := validateTURNCredentials(response); err == nil {
			t.Fatal("invalid response accepted", response)
		}
	}
}

func TestTURNAutoUsesAPIWhenLocalAllocationEligibilityIsUnknown(t *testing.T) {
	for _, permissions := range [][]string{nil, {}, {"turn.credentials.create"}} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			_, _ = io.WriteString(w, turnTestAPIResponse)
		}))
		claims := map[string]any{"type": "pat", "token_endpoint": "token-1", "exp": time.Now().Add(time.Hour).Unix()}
		if permissions != nil {
			claims["permissions"] = permissions
		}
		token := turnTestToken(t, claims)
		result, err := CreateTURNCredentials(t.Context(), CreateTURNCredentialsOptions{Token: token, APIURL: server.URL, ProjectEndpoint: "127", ClusterDomain: "relay.test"})
		server.Close()
		if err != nil || result == nil || calls.Load() != 1 {
			t.Fatalf("local eligibility bypassed: calls=%d err=%v", calls.Load(), err)
		}
	}
}

func TestTURNClaimAliasesAndRequiredPATExpiry(t *testing.T) {
	token := turnTestToken(t, map[string]any{"type": "pat", "tokendpoint": "legacy", "exp": time.Now().Add(time.Hour).Unix()})
	result, err := CreateTURNCredentials(t.Context(), CreateTURNCredentialsOptions{Token: token, Mode: modePtr(TURNCredentialModePAT), ProjectEndpoint: "127", ClusterDomain: "relay.test"})
	if err != nil || !strings.HasSuffix(result.Username, ":127:legacy") {
		t.Fatal("legacy endpoint rejected", err)
	}
	for _, claims := range []map[string]any{
		{"type": "pat", "token_endpoint": "x"},
		{"type": "pat", "token_endpoint": "x", "tokendpoint": "y", "exp": time.Now().Add(time.Hour).Unix()},
	} {
		_, err := CreateTURNCredentials(t.Context(), CreateTURNCredentialsOptions{Token: turnTestToken(t, claims), Mode: modePtr(TURNCredentialModePAT), ProjectEndpoint: "127", ClusterDomain: "relay.test"})
		if err == nil {
			t.Fatal("invalid PAT proof accepted")
		}
	}
}

func TestTURNFallbackRejectsTLSValidationAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, err := range []error{context.Canceled, &controlplane.APIError{StatusCode: 503, Err: errors.New("busy")}, &net.DNSError{IsTimeout: true}} {
		if turnEngineFallbackAllowed(ctx, err) {
			t.Fatal("canceled request allowed fallback")
		}
	}
	for _, err := range []error{context.Canceled, x509.UnknownAuthorityError{}, &tls.CertificateVerificationError{Err: x509.HostnameError{}}, errors.New("invalid JSON schema"), controlplane.ErrAccessProtection} {
		if turnEngineFallbackAllowed(t.Context(), err) {
			t.Fatalf("permanent failure allowed fallback: %v", err)
		}
	}
	if !turnEngineFallbackAllowed(t.Context(), &net.DNSError{IsNotFound: true}) {
		t.Fatal("DNS unavailability prevented fallback")
	}
}
