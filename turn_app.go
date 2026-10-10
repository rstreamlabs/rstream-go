// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func hasTURNAppCredentials(opts CreateTURNCredentialsOptions) bool {
	return opts.ClientID != "" || opts.ClientSecret != ""
}

func turnAppPrivateKey(opts CreateTURNCredentialsOptions) (*ecdsa.PrivateKey, error) {
	if !validTURNComponent(opts.ClientID) || len(opts.ClientSecret) > 16384 {
		return nil, errors.New("invalid TURN APP client ID or private key")
	}
	der, err := hex.DecodeString(opts.ClientSecret)
	if err != nil {
		return nil, errors.New("TURN APP private key must be hexadecimal PKCS8 DER")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, errors.New("invalid TURN APP PKCS8 private key")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("TURN APP private key must be an EC key")
	}
	return key, nil
}

func loadTURNAppPublicKey(ctx context.Context, opts CreateTURNCredentialsOptions, realm string) (string, error) {
	if opts.TURNServerPublicKeyHex != "" {
		return opts.TURNServerPublicKeyHex, nil
	}
	base := strings.TrimSpace(opts.TURNKeyringBaseURL)
	if base == "" {
		base = strings.TrimSpace(opts.APIURL)
	}
	if base == "" {
		base = "https://rstream.io"
	}
	target, err := url.Parse(base)
	if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return "", errors.New("TURN keyring requires an HTTPS origin without credentials")
	}
	target.Path, target.RawPath = "/keyrings/turn/"+realm+".spki.der.hex", ""
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second}
	if opts.HTTPClient != nil {
		if configured, ok := opts.HTTPClient.Transport.(*http.Transport); ok && configured.TLSClientConfig != nil && configured.TLSClientConfig.RootCAs != nil {
			transport.TLSClientConfig = &tls.Config{RootCAs: configured.TLSClientConfig.RootCAs.Clone()}
		}
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("TURN keyring returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil {
		return "", err
	}
	if len(body) > 8192 {
		return "", errors.New("TURN keyring exceeds 8 KiB")
	}
	return strings.TrimSpace(string(body)), nil
}

func createAPPTURNCredentials(ctx context.Context, opts CreateTURNCredentialsOptions) (*TURNCredentials, error) {
	if !validTURNComponent(opts.ProjectEndpoint) || opts.TURNPort < 0 || opts.TURNPort > 65535 || opts.TURNSPort < 0 || opts.TURNSPort > 65535 {
		return nil, errors.New("invalid TURN project or port")
	}
	domain, realm := normalizeTURNClusterDomain(opts.TURNDomain), normalizeTURNClusterDomain(opts.TURNRealm)
	if domain == "" {
		domain = normalizeTURNClusterDomain(opts.ClusterDomain)
	}
	if realm == "" {
		realm = normalizeTURNClusterDomain(opts.ClusterDomain)
	}
	if !validTURNDomain(domain) || !validTURNDomain(realm) {
		return nil, errors.New("TURN APP mode requires a valid relay domain and realm")
	}
	privateKey, err := turnAppPrivateKey(opts)
	if err != nil {
		return nil, err
	}
	publicHex, err := loadTURNAppPublicKey(ctx, opts, realm)
	if err != nil {
		return nil, err
	}
	return deriveAPPTURNCredentials(opts, privateKey, publicHex, domain, realm, time.Now())
}

func deriveAPPTURNCredentials(opts CreateTURNCredentialsOptions, privateKey *ecdsa.PrivateKey, publicHex, domain, realm string, now time.Time) (*TURNCredentials, error) {
	if len(publicHex) > 8192 {
		return nil, errors.New("TURN server public key exceeds 8 KiB")
	}
	der, err := hex.DecodeString(publicHex)
	if err != nil {
		return nil, errors.New("TURN server public key must be hexadecimal SPKI DER")
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, errors.New("invalid TURN server SPKI public key")
	}
	publicKey, ok := parsed.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve != privateKey.Curve {
		return nil, errors.New("TURN APP client and server keys must use the same EC curve")
	}
	clientECDH, err := privateKey.ECDH()
	if err != nil {
		return nil, err
	}
	serverECDH, err := publicKey.ECDH()
	if err != nil {
		return nil, err
	}
	shared, err := clientECDH.ECDH(serverECDH)
	if err != nil {
		return nil, err
	}
	key, err := hkdf.Key(sha256.New, shared, []byte(realm), "turn-app-v1", 32)
	if err != nil {
		return nil, err
	}
	ttl := normalizeTURNCredentialTTL(opts.TTL)
	username := fmt.Sprintf("v1:%d:app:%s:%s", now.Unix()+int64(ttl/time.Second), opts.ProjectEndpoint, opts.ClientID)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(username))
	return &TURNCredentials{Username: username, Credential: base64.StdEncoding.EncodeToString(mac.Sum(nil)), URLs: turnURLs(domain, opts.TURNPort, opts.TURNSPort), TTL: int(ttl / time.Second)}, nil
}

func createTURNAppToken(opts CreateTURNCredentialsOptions) (string, error) {
	key, err := turnAppPrivateKey(opts)
	if err != nil {
		return "", err
	}
	if key.Curve != elliptic.P521() {
		return "", errors.New("TURN APP API authentication requires a P-521 private key")
	}
	now := time.Now().Unix()
	claims, err := json.Marshal(struct {
		Type        string   `json:"type"`
		ClientID    string   `json:"clientId"`
		IssuedAt    int64    `json:"iat"`
		ExpiresAt   int64    `json:"exp"`
		Permissions []string `json:"permissions"`
	}{"app", opts.ClientID, now, now + 60, []string{"turn.credentials.create"}})
	if err != nil {
		return "", err
	}
	message := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES512","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha512.Sum512([]byte(message))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	signature := make([]byte, 132)
	r.FillBytes(signature[:66])
	s.FillBytes(signature[66:])
	return message + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
