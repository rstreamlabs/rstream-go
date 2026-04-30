// See LICENSE file in the project root for license information.

package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

const OAuthDeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

type OAuthError struct {
	Code        string
	Description string
}

type OAuthAuthorizationServerMetadata struct {
	Issuer                      string   `json:"issuer"`
	DeviceAuthorizationEndpoint string   `json:"device_authorization_endpoint"`
	TokenEndpoint               string   `json:"token_endpoint"`
	RevocationEndpoint          string   `json:"revocation_endpoint"`
	GrantTypesSupported         []string `json:"grant_types_supported"`
	ScopesSupported             []string `json:"scopes_supported"`
}

type OAuthDeviceAuthorizationRequest struct {
	ClientID string
	Scope    string
	Source   []RstreamLabel
}

type OAuthDeviceAuthorizationResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval,omitempty"`
}

type OAuthDeviceTokenRequest struct {
	ClientID   string
	DeviceCode string
}

type OAuthDeviceTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

func (e *OAuthError) Error() string {
	if e.Description != "" {
		return e.Code + ": " + e.Description
	}
	return e.Code
}

func (r OAuthDeviceAuthorizationRequest) values() (url.Values, error) {
	values := url.Values{}
	values.Set("client_id", r.ClientID)
	if strings.TrimSpace(r.Scope) != "" {
		values.Set("scope", strings.TrimSpace(r.Scope))
	}
	if len(r.Source) > 0 {
		payload, err := json.Marshal(r.Source)
		if err != nil {
			return nil, err
		}
		values.Set("rstream_source", string(payload))
	}
	return values, nil
}

func (r OAuthDeviceTokenRequest) values() url.Values {
	values := url.Values{}
	values.Set("grant_type", OAuthDeviceGrantType)
	values.Set("client_id", r.ClientID)
	values.Set("device_code", r.DeviceCode)
	return values
}

func tokenRevocationValues(token string) url.Values {
	values := url.Values{}
	values.Set("token", token)
	values.Set("token_type_hint", "access_token")
	return values
}

func (c *Client) OAuthAuthorizationServerMetadata(ctx context.Context) (OAuthAuthorizationServerMetadata, error) {
	var out OAuthAuthorizationServerMetadata
	_, err := c.doJSON(ctx, http.MethodGet, "/.well-known/oauth-authorization-server", nil, &out)
	return out, err
}

func (c *Client) CreateOAuthDeviceAuthorization(ctx context.Context, endpoint string, req OAuthDeviceAuthorizationRequest) (OAuthDeviceAuthorizationResponse, error) {
	var out OAuthDeviceAuthorizationResponse
	values, err := req.values()
	if err != nil {
		return out, err
	}
	_, err = c.doForm(ctx, http.MethodPost, endpoint, values, &out)
	return out, err
}

func (c *Client) ExchangeOAuthDeviceToken(ctx context.Context, endpoint string, req OAuthDeviceTokenRequest) (OAuthDeviceTokenResponse, error) {
	var out OAuthDeviceTokenResponse
	_, err := c.doForm(ctx, http.MethodPost, endpoint, req.values(), &out)
	return out, err
}

func (c *Client) RevokeOAuthToken(ctx context.Context, endpoint, token string) error {
	_, err := c.doForm(ctx, http.MethodPost, endpoint, tokenRevocationValues(token), nil)
	return err
}
