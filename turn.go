// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/internal/fipsprofile"
)

const defaultTURNPort = 3478
const defaultTURNSPort = 5349
const defaultTURNCredentialTTL = 10 * time.Minute
const maxTURNCredentialTTL = time.Hour

type TURNCredentials = controlplane.TURNCredentials

type TURNCredentialMode string

const (
	TURNCredentialModeAuto   TURNCredentialMode = "auto"
	TURNCredentialModeAPI    TURNCredentialMode = "api"
	TURNCredentialModeEngine TURNCredentialMode = "engine"
	TURNCredentialModePAT    TURNCredentialMode = "pat"
	TURNCredentialModeAPP    TURNCredentialMode = "app"
)

type CreateTURNCredentialsOptions struct {
	// Client reuses Engine connections and in-memory discovery across renewals.
	Client                 *Client
	APIURL                 string
	Token                  string
	ClientID               string
	ClientSecret           string
	TURNServerPublicKeyHex string
	TURNKeyringBaseURL     string
	ProjectID              string
	ProjectEndpoint        string
	// Deprecated: use TURNDomain and TURNRealm when the relay host and authentication realm may differ.
	ClusterDomain       string
	TURNDomain          string
	TURNRealm           string
	TURNPort            int
	TURNSPort           int
	TTL                 time.Duration
	Mode                *TURNCredentialMode
	HTTPClient          *http.Client
	ControlPlaneHeaders map[string]string
}

type turnTokenClaims struct {
	AllPermissions      bool     `json:"-"`
	Type                string   `json:"type"`
	TokenEndpoint       string   `json:"token_endpoint,omitempty"`
	LegacyTokenEndpoint string   `json:"tokendpoint,omitempty"`
	Permissions         []string `json:"permissions,omitempty"`
	ExpiresAt           *int64   `json:"exp,omitempty"`
}

func CreateTURNCredentials(ctx context.Context, opts CreateTURNCredentialsOptions) (*TURNCredentials, error) {
	if opts.Mode != nil && *opts.Mode == TURNCredentialModeAuto {
		opts.Mode = nil
	}
	if ctx == nil {
		return nil, errors.New("TURN request context is required")
	}
	if err := validateTURNTTL(opts.TTL); err != nil {
		return nil, err
	}
	if opts.Client != nil {
		return opts.Client.CreateTURNCredentials(ctx, opts)
	}
	if err := fipsprofile.Unavailable("TURN support"); err != nil {
		return nil, err
	}
	if hasTURNAppCredentials(opts) {
		if strings.TrimSpace(opts.Token) != "" {
			return nil, errors.New("token and APP credentials cannot be used together")
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if opts.Mode != nil && *opts.Mode == TURNCredentialModeAPP || opts.Mode == nil && turnLocalTargetAvailable(opts) {
			return createAPPTURNCredentials(ctx, opts)
		}
		token, err := createTURNAppToken(opts)
		if err != nil {
			return nil, err
		}
		opts.Token = token
	}
	token := strings.TrimSpace(opts.Token)
	if token == "" {
		return nil, errors.New("token is required")
	}
	mode, claims, err := resolveTURNCredentialMode(token, opts.Mode)
	if err != nil {
		return nil, err
	}
	if mode == TURNCredentialModeAPI || opts.Mode == nil && !turnLocalTargetAvailable(opts) {
		return createAPITURNCredentials(ctx, opts, token)
	}
	return createPATTURNCredentials(opts, token, claims)
}

func createAPITURNCredentials(ctx context.Context, opts CreateTURNCredentialsOptions, token string) (*TURNCredentials, error) {
	apiURL := strings.TrimSpace(opts.APIURL)
	if apiURL == "" {
		return nil, errors.New("API URL is required for TURN API mode")
	}
	projectID := strings.TrimSpace(opts.ProjectID)
	projectEndpoint := strings.TrimSpace(opts.ProjectEndpoint)
	if projectID == "" && projectEndpoint == "" {
		return nil, errors.New("project ID or project endpoint is required for TURN API mode")
	}
	clientOpts := make([]controlplane.Option, 0, 2)
	clientOpts = append(clientOpts, controlplane.WithHeaders(opts.ControlPlaneHeaders))
	if opts.HTTPClient != nil {
		clientOpts = append(clientOpts, controlplane.WithHTTPClient(opts.HTTPClient))
	}
	client := controlplane.NewClient(apiURL, token, clientOpts...)
	if opts.HTTPClient == nil {
		defer client.CloseIdleConnections()
	}
	request := controlplane.CreateTURNCredentialsRequest{}
	if opts.TTL > 0 {
		ttlSeconds := int(normalizeTURNCredentialTTL(opts.TTL) / time.Second)
		request.TTLSeconds = &ttlSeconds
	}
	var res TURNCredentials
	var err error
	if projectID != "" {
		res, err = client.CreateProjectTURNCredentialsWithOptions(ctx, projectID, request)
	} else {
		res, err = client.CreateProjectTURNCredentialsByEndpointWithOptions(ctx, projectEndpoint, request)
	}
	if err != nil {
		return nil, err
	}
	if err := validateTURNCredentials(res); err != nil {
		return nil, err
	}
	return &res, nil
}

func createPATTURNCredentials(opts CreateTURNCredentialsOptions, token string, claims turnTokenClaims) (*TURNCredentials, error) {
	return createPATTURNCredentialsAt(opts, token, claims, time.Now())
}

func createPATTURNCredentialsAt(opts CreateTURNCredentialsOptions, token string, claims turnTokenClaims, now time.Time) (*TURNCredentials, error) {
	projectEndpoint := strings.TrimSpace(opts.ProjectEndpoint)
	if projectEndpoint == "" {
		return nil, errors.New("project endpoint is required for TURN PAT mode")
	}
	if !validTURNComponent(projectEndpoint) || opts.TURNPort < 0 || opts.TURNPort > 65535 || opts.TURNSPort < 0 || opts.TURNSPort > 65535 {
		return nil, errors.New("invalid TURN project or port")
	}
	legacyDomain := normalizeTURNClusterDomain(opts.ClusterDomain)
	turnDomain := normalizeTURNClusterDomain(opts.TURNDomain)
	if turnDomain == "" {
		turnDomain = legacyDomain
	}
	turnRealm := normalizeTURNClusterDomain(opts.TURNRealm)
	if turnRealm == "" {
		turnRealm = legacyDomain
	}
	if turnDomain == "" {
		return nil, errors.New("TURN domain is required for TURN PAT mode")
	}
	if turnRealm == "" {
		return nil, errors.New("TURN realm is required for TURN PAT mode")
	}
	if !validTURNDomain(turnDomain) || !validTURNDomain(turnRealm) {
		return nil, errors.New("invalid TURN domain or realm")
	}
	ttl, err := normalizePATTURNCredentialTTL(opts.TTL, claims, now)
	if err != nil {
		return nil, err
	}
	username := fmt.Sprintf(
		"v1:%d:pat:%s:%s",
		now.Add(ttl).Unix(),
		projectEndpoint,
		claims.TokenEndpoint,
	)
	tokenHash := sha256.Sum256([]byte(token))
	key, err := hkdf.Key(sha256.New, tokenHash[:], []byte(turnRealm), "turn-pat-v1", 32)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(username))
	return &TURNCredentials{
		Username:   username,
		Credential: base64.StdEncoding.EncodeToString(mac.Sum(nil)),
		URLs:       turnURLs(turnDomain, opts.TURNPort, opts.TURNSPort),
		TTL:        int(ttl / time.Second),
	}, nil
}

func resolveTURNCredentialMode(token string, requested *TURNCredentialMode) (TURNCredentialMode, turnTokenClaims, error) {
	if requested != nil {
		switch *requested {
		case TURNCredentialModeAPI:
			return TURNCredentialModeAPI, turnTokenClaims{}, nil
		case TURNCredentialModePAT:
			claims, err := parseTURNTokenClaims(token)
			if err != nil {
				return "", turnTokenClaims{}, err
			}
			if claims.Type != "pat" {
				return "", turnTokenClaims{}, errors.New("TURN PAT mode requires a PAT token")
			}
			if strings.TrimSpace(claims.TokenEndpoint) == "" {
				return "", turnTokenClaims{}, errors.New("TURN PAT mode requires a PAT token carrying a token endpoint")
			}
			return TURNCredentialModePAT, claims, nil
		default:
			return "", turnTokenClaims{}, fmt.Errorf("invalid TURN credential mode %q", *requested)
		}
	}
	claims, err := parseTURNTokenClaims(token)
	if err != nil {
		if errors.Is(err, errTURNEndpointConflict) {
			return "", turnTokenClaims{}, err
		}
		return TURNCredentialModeAPI, turnTokenClaims{}, nil
	}
	if claims.Type == "pat" && claims.TokenEndpoint != "" && claims.ExpiresAt != nil && (claims.AllPermissions || slices.Contains(claims.Permissions, "turn.relay.allocate")) {
		return TURNCredentialModePAT, claims, nil
	}
	return TURNCredentialModeAPI, claims, nil
}

func parseTURNTokenClaims(token string) (turnTokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return turnTokenClaims{}, errors.New("invalid token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return turnTokenClaims{}, errors.New("invalid token format")
	}
	var claims turnTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return turnTokenClaims{}, errors.New("invalid token format")
	}
	var rawClaims struct {
		Permissions json.RawMessage `json:"permissions"`
	}
	if err := json.Unmarshal(payload, &rawClaims); err != nil {
		return turnTokenClaims{}, errors.New("invalid token format")
	}
	claims.AllPermissions = strings.TrimSpace(string(rawClaims.Permissions)) == "null"
	if claims.TokenEndpoint != "" && claims.LegacyTokenEndpoint != "" && claims.TokenEndpoint != claims.LegacyTokenEndpoint {
		return turnTokenClaims{}, errTURNEndpointConflict
	}
	if claims.TokenEndpoint == "" {
		claims.TokenEndpoint = claims.LegacyTokenEndpoint
	}
	if claims.TokenEndpoint != "" && !validTURNComponent(claims.TokenEndpoint) {
		return turnTokenClaims{}, errors.New("invalid TURN token endpoint")
	}
	if strings.TrimSpace(claims.Type) == "" {
		return turnTokenClaims{}, errors.New("invalid token format")
	}
	return claims, nil
}

func normalizeTURNClusterDomain(domain string) string {
	return strings.ToLower(strings.TrimSpace(domain))
}

func normalizeTURNCredentialTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultTURNCredentialTTL
	}
	if ttl > maxTURNCredentialTTL {
		return maxTURNCredentialTTL
	}
	return ttl
}

func normalizePATTURNCredentialTTL(ttl time.Duration, claims turnTokenClaims, now time.Time) (time.Duration, error) {
	ttl = normalizeTURNCredentialTTL(ttl)
	if claims.ExpiresAt == nil {
		return 0, errors.New("TURN PAT mode requires an expiring PAT token")
	}
	remaining := time.Until(time.Unix(*claims.ExpiresAt, 0))
	if !now.IsZero() {
		remaining = time.Duration(*claims.ExpiresAt-now.Unix()) * time.Second
	}
	if remaining <= 0 {
		return 0, errors.New("PAT token is expired")
	}
	if remaining < ttl {
		return remaining, nil
	}
	return ttl, nil
}

func normalizeTURNPort(port, fallback int) int {
	if port > 0 {
		return port
	}
	return fallback
}

func turnURLs(clusterDomain string, turnPort, turnsPort int) []string {
	return []string{
		fmt.Sprintf("turn:%s:%d?transport=udp", clusterDomain, normalizeTURNPort(turnPort, defaultTURNPort)),
		fmt.Sprintf("turn:%s:%d?transport=tcp", clusterDomain, normalizeTURNPort(turnPort, defaultTURNPort)),
		fmt.Sprintf("turns:%s:%d?transport=udp", clusterDomain, normalizeTURNPort(turnsPort, defaultTURNSPort)),
		fmt.Sprintf("turns:%s:%d?transport=tcp", clusterDomain, normalizeTURNPort(turnsPort, defaultTURNSPort)),
	}
}
