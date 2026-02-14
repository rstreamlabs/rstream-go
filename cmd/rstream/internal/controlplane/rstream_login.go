// See LICENSE file in the project root for license information.

package controlplane

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type RstreamLabel struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

type RstreamLoginRequest struct {
	Permissions []string       `json:"permissions"`
	Source      []RstreamLabel `json:"source,omitempty"`
}

type RstreamLoginResponse struct {
	RequestID     string     `json:"requestId"`
	RequestSecret string     `json:"requestSecret"`
	URL           string     `json:"url"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
}

type RstreamLoginTokenRequest struct {
	RequestSecret string `json:"requestSecret"`
}

type RstreamLoginTokenResponse struct {
	Status    string     `json:"status"`
	Token     string     `json:"token,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

func (c *Client) CreateRstreamLogin(ctx context.Context, req RstreamLoginRequest) (RstreamLoginResponse, error) {
	var out RstreamLoginResponse
	_, err := c.doJSONBody(ctx, http.MethodPost, "/api/rstream/login/requests", nil, req, &out)
	return out, err
}

func (c *Client) ExchangeRstreamLoginToken(ctx context.Context, requestID string, req RstreamLoginTokenRequest) (RstreamLoginTokenResponse, error) {
	var out RstreamLoginTokenResponse
	path := "/api/rstream/login/requests/" + url.PathEscape(requestID) + "/token"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, req, &out)
	return out, err
}
