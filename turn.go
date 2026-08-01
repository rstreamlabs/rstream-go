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
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go/controlplane"
)

const defaultTURNPort = 3478
const defaultTURNSPort = 5349
const defaultTURNCredentialTTL = 10 * time.Minute
const maxTURNCredentialTTL = time.Hour

type TURNCredentials = controlplane.TURNCredentials

type TURNCredentialMode string

const (
	TURNCredentialModeAPI TURNCredentialMode = "api"
	TURNCredentialModePAT TURNCredentialMode = "pat"
)

type CreateTURNCredentialsOptions struct {
	APIURL              string
	Token               string
	ProjectID           string
	ProjectEndpoint     string
	ClusterDomain       string
	TURNPort            int
	TURNSPort           int
	TTL                 time.Duration
	Mode                *TURNCredentialMode
	HTTPClient          *http.Client
	ControlPlaneHeaders map[string]string
}

type turnTokenClaims struct {
	Type          string `json:"type"`
	TokenEndpoint string `json:"token_endpoint,omitempty"`
	ExpiresAt     *int64 `json:"exp,omitempty"`
}

func CreateTURNCredentials(ctx context.Context, opts CreateTURNCredentialsOptions) (*TURNCredentials, error) {
	token := strings.TrimSpace(opts.Token)
	if token == "" {
		return nil, errors.New("token is required")
	}
	mode, claims, err := resolveTURNCredentialMode(token, opts.Mode)
	if err != nil {
		return nil, err
	}
	if mode == TURNCredentialModeAPI {
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
	return &res, nil
}

func createPATTURNCredentials(opts CreateTURNCredentialsOptions, token string, claims turnTokenClaims) (*TURNCredentials, error) {
	projectEndpoint := strings.TrimSpace(opts.ProjectEndpoint)
	if projectEndpoint == "" {
		return nil, errors.New("project endpoint is required for TURN PAT mode")
	}
	clusterDomain := normalizeTURNClusterDomain(opts.ClusterDomain)
	if clusterDomain == "" {
		return nil, errors.New("cluster domain is required for TURN PAT mode")
	}
	now := time.Now()
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
	key, err := hkdf.Key(sha256.New, tokenHash[:], []byte(clusterDomain), "turn-pat-v1", 32)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(username))
	return &TURNCredentials{
		Username:   username,
		Credential: base64.StdEncoding.EncodeToString(mac.Sum(nil)),
		URLs:       turnURLs(clusterDomain, opts.TURNPort, opts.TURNSPort),
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
		return TURNCredentialModeAPI, turnTokenClaims{}, nil
	}
	if claims.Type == "pat" && claims.TokenEndpoint != "" {
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
		return ttl, nil
	}
	remaining := time.Until(time.Unix(*claims.ExpiresAt, 0))
	if !now.IsZero() {
		remaining = time.Unix(*claims.ExpiresAt, 0).Sub(now)
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
