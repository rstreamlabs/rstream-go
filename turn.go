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
const defaultTURNCredentialTTL = 24 * time.Hour

type TURNCredentials = controlplane.TURNCredentials

type TURNCredentialMode string

const (
	TURNCredentialModeAPI TURNCredentialMode = "api"
	TURNCredentialModePAT TURNCredentialMode = "pat"
)

type CreateTURNCredentialsOptions struct {
	APIURL          string
	Token           string
	ProjectID       string
	ProjectEndpoint string
	ClusterDomain   string
	TURNPort        int
	TURNSPort       int
	TTL             time.Duration
	Mode            *TURNCredentialMode
	HTTPClient      *http.Client
}

type turnTokenClaims struct {
	Type          string `json:"type"`
	TokenEndpoint string `json:"token_endpoint,omitempty"`
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
	clientOpts := make([]controlplane.Option, 0, 1)
	if opts.HTTPClient != nil {
		clientOpts = append(clientOpts, controlplane.WithHTTPClient(opts.HTTPClient))
	}
	client := controlplane.NewClient(apiURL, token, clientOpts...)
	var res TURNCredentials
	var err error
	if projectID != "" {
		res, err = client.CreateProjectTURNCredentials(ctx, projectID)
	} else {
		res, err = client.CreateProjectTURNCredentialsByEndpoint(ctx, projectEndpoint)
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
	ttl := normalizeTURNCredentialTTL(opts.TTL)
	username := fmt.Sprintf(
		"v1:%d:pat:%s:%s",
		time.Now().Unix()+int64(ttl/time.Second),
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
	claims, err := parseTURNTokenClaims(token)
	if err != nil {
		return "", turnTokenClaims{}, err
	}
	if requested == nil {
		if claims.Type == "pat" && claims.TokenEndpoint != "" {
			return TURNCredentialModePAT, claims, nil
		}
		return TURNCredentialModeAPI, claims, nil
	}
	switch *requested {
	case TURNCredentialModeAPI:
		return TURNCredentialModeAPI, claims, nil
	case TURNCredentialModePAT:
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
	return ttl
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
