// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/internal/fipsprofile"
)

// CreateTURNCredentials reuses this client's authentication, transport and
// discovery cache. Explicit api mode selects the Control plane; engine selects
// the Engine API. Certificate clients use the Engine directly in automatic mode.
func (c *Client) CreateTURNCredentials(ctx context.Context, opts CreateTURNCredentialsOptions) (*TURNCredentials, error) {
	if opts.Mode != nil && *opts.Mode == TURNCredentialModeAuto {
		opts.Mode = nil
	}
	if ctx == nil {
		return nil, errors.New("TURN request context is required")
	}
	if err := fipsprofile.Unavailable("TURN support"); err != nil {
		return nil, err
	}
	if err := validateTURNTTL(opts.TTL); err != nil {
		return nil, err
	}
	if c == nil || c.closed.Load() {
		return nil, net.ErrClosed
	}
	if opts.Client != nil && opts.Client != c {
		return nil, errors.New("conflicting TURN clients")
	}
	opts.Client = nil
	if c.Token != nil && *c.Token != "" {
		if opts.Token != "" && opts.Token != *c.Token {
			return nil, errors.New("TURN token must match the Engine client identity")
		}
		opts.Token = *c.Token
	}
	certificate := tlsConfigHasClientCertificate(c.TLSClientConfig)
	application := hasTURNAppCredentials(opts)
	if application && (certificate || opts.Token != "") {
		return nil, errors.New("APP credentials cannot be combined with a token or certificate")
	}
	if certificate && opts.Token != "" {
		return nil, errors.New("token and mTLS authentication cannot be used together")
	}
	if !certificate && opts.Token == "" && !application {
		return nil, errors.New("token is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx, finish, err := c.beginDial(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if application {
		if opts.Mode != nil && *opts.Mode == TURNCredentialModeAPP || opts.Mode == nil && turnLocalTargetAvailable(opts) {
			return createAPPTURNCredentials(ctx, opts)
		}
		token, tokenErr := createTURNAppToken(opts)
		if tokenErr != nil {
			return nil, tokenErr
		}
		opts.Token = token
		opts.ClientID, opts.ClientSecret = "", ""
	}
	if opts.Mode != nil && *opts.Mode == TURNCredentialModeEngine {
		return c.createEngineTURNCredentials(ctx, opts)
	}
	if certificate {
		if opts.Mode != nil {
			return nil, errors.New("certificate authentication requires automatic or engine TURN mode")
		}
		return c.createEngineTURNCredentials(ctx, opts)
	}
	if opts.Mode != nil {
		return CreateTURNCredentials(ctx, opts)
	}
	mode, claims, err := resolveTURNCredentialMode(opts.Token, nil)
	if err != nil {
		return nil, err
	}
	if mode == TURNCredentialModePAT && turnLocalTargetAvailable(opts) {
		return createPATTURNCredentials(opts, opts.Token, claims)
	}
	if strings.TrimSpace(opts.APIURL) == "" {
		return c.createEngineTURNCredentials(ctx, opts)
	}
	attempt, cancelAttempt := context.WithTimeout(ctx, 5*time.Second)
	result, err := createAPITURNCredentials(attempt, opts, opts.Token)
	cancelAttempt()
	if err == nil || !turnEngineFallbackAllowed(ctx, err) {
		return result, err
	}
	if strings.TrimSpace(opts.ProjectEndpoint) == "" {
		return nil, err
	}
	return c.createEngineTURNCredentials(ctx, opts)
}

func (c *Client) createEngineTURNCredentials(ctx context.Context, opts CreateTURNCredentialsOptions) (*TURNCredentials, error) {
	engine, err := c.getEngine()
	if err != nil {
		return nil, err
	}
	u, err := url.Parse("https://" + strings.TrimPrefix(*engine, "https://"))
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("invalid Engine address")
	}
	endpoint := strings.Split(u.Hostname(), ".")[0]
	if opts.ProjectEndpoint != "" && opts.ProjectEndpoint != endpoint {
		return nil, errors.New("TURN project endpoint does not match the Engine client")
	}
	if opts.ProjectID != "" && opts.ProjectEndpoint == "" {
		return nil, errors.New("project endpoint is required to bind a project ID to the Engine client")
	}
	request := controlplane.CreateTURNCredentialsRequest{}
	if opts.TTL != 0 {
		ttl := int(opts.TTL / time.Second)
		request.TTLSeconds = &ttl
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var token *string
	if opts.Token != "" {
		token = &opts.Token
	}
	data, _, err := c.apiDo(ctx, http.MethodPost, "/turn-server/credentials", nil, strings.NewReader(string(payload)), nil, token)
	if err != nil {
		return nil, err
	}
	if len(data) > 16384 {
		return nil, errors.New("TURN response is too large")
	}
	var response TURNCredentials
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, errors.New("invalid TURN response")
	}
	if err := validateTURNCredentials(response); err != nil {
		return nil, err
	}
	return &response, nil
}

func turnLocalTargetAvailable(opts CreateTURNCredentialsOptions) bool {
	return strings.TrimSpace(opts.ProjectEndpoint) != "" && ((opts.TURNDomain != "" && opts.TURNRealm != "") || opts.ClusterDomain != "")
}

func validateTURNTTL(ttl time.Duration) error {
	if ttl != 0 && (ttl < time.Second || ttl > time.Hour || ttl%time.Second != 0) {
		return errors.New("TURN TTL must be a whole number of seconds between 1 and 3600")
	}
	return nil
}

func validateTURNCredentials(value TURNCredentials) error {
	if value.Username == "" || len(value.Username) > 512 || value.Credential == "" || len(value.Credential) > 1024 || value.TTL < 1 || value.TTL > 3600 || len(value.URLs) == 0 || len(value.URLs) > 16 {
		return errors.New("invalid TURN credential response")
	}
	for _, raw := range value.URLs {
		u, err := url.Parse(raw)
		if err != nil || len(raw) > 2048 || (u.Scheme != "turn" && u.Scheme != "turns" && u.Scheme != "stun" && u.Scheme != "stuns") || u.Opaque == "" || strings.ContainsAny(raw, "\r\n\t ") {
			return errors.New("invalid TURN server URL")
		}
	}
	return nil
}

func turnEngineFallbackAllowed(ctx context.Context, err error) bool {
	if context.Cause(ctx) != nil || errors.Is(err, context.Canceled) {
		return false
	}
	var apiError *controlplane.APIError
	if errors.As(err, &apiError) {
		return apiError.StatusCode == 502 || apiError.StatusCode == 503 || apiError.StatusCode == 504
	}
	if !retryableAPITransportError(ctx, 0, err) {
		var dns *net.DNSError
		return errors.As(err, &dns)
	}
	var networkError net.Error
	return errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

var errTURNEndpointConflict = errors.New("conflicting token_endpoint and tokendpoint claims")

func validTURNComponent(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func validTURNDomain(value string) bool {
	if len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
