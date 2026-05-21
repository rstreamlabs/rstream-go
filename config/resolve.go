// See LICENSE file in the project root for license information.

package config

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
)

const defaultAPIURL = "https://rstream.io"

func DefaultAPIURL() string {
	return defaultAPIURL
}

const (
	TokenStorageInline      = "inline"
	TokenStorageKeychain    = "keychain"
	CredentialProviderMacOS = "macos"
	MTLSStorageKeychain     = "keychain"
	MTLSStoragePKCS11       = "pkcs11"
)

type ResolveInput struct {
	Config               Config
	FlagAPIURL           string
	FlagContext          string
	FlagEngine           string
	FlagToken            string
	FlagMTLSCert         string
	FlagMTLSKey          string
	EnvAPIURL            string
	EnvContext           string
	EnvEngine            string
	EnvToken             string
	EnvMTLSCert          string
	EnvMTLSKey           string
	IgnoreDefaultContext bool
	RequireToken         bool
	RequireEngine        bool
	ResolveToken         bool
}

type Resolved struct {
	APIURL          string
	ContextName     string
	Environment     *Environment
	Context         *Context
	Engine          string
	Token           string
	Transport       rstream.Dialer
	TLSClientConfig *tls.Config
}

func Resolve(input ResolveInput) (Resolved, error) {
	cfg := input.Config
	apiURLExplicit := NormalizeAPIURL(firstNonEmpty(input.FlagAPIURL, input.EnvAPIURL))
	contextName := firstNonEmpty(input.FlagContext, input.EnvContext)
	if !input.IgnoreDefaultContext && contextName == "" && cfg.Defaults.Context != nil {
		contextName = cfg.Defaults.Context.Name
	}
	var ctx *Context
	if contextName != "" {
		var err error
		switch {
		case apiURLExplicit != "":
			ctx, _, err = cfg.FindContextByNameAndAPIURL(contextName, apiURLExplicit)
			if err != nil {
				return Resolved{}, err
			}
			if ctx == nil {
				ctx, _, err = cfg.FindContextUnlinked(contextName)
				if err != nil {
					return Resolved{}, err
				}
			}
		default:
			ctx, _, err = cfg.FindContextByName(contextName)
		}
		if err != nil {
			return Resolved{}, err
		}
		if ctx == nil {
			if apiURLExplicit != "" {
				return Resolved{}, fmt.Errorf("context %q not found for API URL %q", contextName, apiURLExplicit)
			}
			return Resolved{}, fmt.Errorf("context %q not found", contextName)
		}
		ctxAPIURL := NormalizeAPIURL(ctx.APIURL)
		if apiURLExplicit != "" && ctxAPIURL != "" && ctxAPIURL != apiURLExplicit {
			return Resolved{}, fmt.Errorf("context %q belongs to API URL %q (selected API URL %q)", contextName, ctx.APIURL, apiURLExplicit)
		}
		if apiURLExplicit == "" && ctxAPIURL != "" {
			apiURLExplicit = ctxAPIURL
		}
	}
	apiURL := apiURLExplicit
	if apiURL == "" {
		apiURL = defaultAPIURL
	}
	env, _ := cfg.FindEnvironment(apiURL)
	if ctx != nil && NormalizeAPIURL(ctx.APIURL) == "" {
		env = nil
	}
	engineOverride := firstNonEmpty(input.FlagEngine, input.EnvEngine)
	engine := engineOverride
	if engine == "" && ctx != nil {
		engine = ctx.Engine
	}
	token := ""
	explicitToken := input.FlagToken != "" || input.EnvToken != ""
	explicitMTLS := input.FlagMTLSCert != "" || input.EnvMTLSCert != "" || input.FlagMTLSKey != "" || input.EnvMTLSKey != ""
	shouldResolveToken := input.ResolveToken || input.RequireToken || explicitToken
	if shouldResolveToken {
		token = firstNonEmpty(input.FlagToken, input.EnvToken)
		if token == "" && !explicitMTLS {
			var err error
			token, err = resolveToken(ctx, env)
			if err != nil {
				return Resolved{}, err
			}
			if token != "" && engineOverrideUsesStoredToken(engineOverride, ctx) {
				return Resolved{}, errors.New("refusing to use a stored token with an explicit engine override; set RSTREAM_AUTHENTICATION_TOKEN or pass --token for the selected engine")
			}
		}
	}
	mtlsConfig, mtlsFromStoredConfig, err := resolveMTLSConfig(input, ctx, env)
	if err != nil {
		return Resolved{}, err
	}
	if token != "" && mtlsConfig != nil {
		return Resolved{}, errors.New("token and mTLS authentication cannot be used together")
	}
	if mtlsConfig != nil && engineOverrideUsesStoredToken(engineOverride, ctx) && mtlsFromStoredConfig {
		return Resolved{}, errors.New("refusing to use stored mTLS credentials with an explicit engine override; set RSTREAM_MTLS_CERT_FILE and RSTREAM_MTLS_KEY_FILE for the selected engine")
	}
	if token != "" {
		expired, err := isTokenExpired(token, time.Now())
		if err != nil {
			return Resolved{}, err
		}
		if expired {
			return Resolved{}, errors.New("token has expired (run rstream login or set RSTREAM_AUTHENTICATION_TOKEN)")
		}
	}
	if input.RequireEngine && engine == "" {
		return Resolved{}, errors.New("engine is required but not configured (set --engine or RSTREAM_ENGINE, or select a context via --context, RSTREAM_CONTEXT, or `rstream context use`)")
	}
	if input.RequireToken && token == "" && mtlsConfig == nil {
		return Resolved{}, errors.New("authentication is required but not configured (run rstream login, set RSTREAM_AUTHENTICATION_TOKEN, or set RSTREAM_MTLS_CERT_FILE and RSTREAM_MTLS_KEY_FILE)")
	}
	var transport rstream.Dialer
	if ctx != nil {
		var merged *TransportConfig
		if env != nil && NormalizeAPIURL(ctx.APIURL) != "" && NormalizeAPIURL(ctx.APIURL) == NormalizeAPIURL(env.APIURL) {
			merged = MergeTransport(envTransport(env), ctxTransport(ctx))
		} else {
			merged = MergeTransport(nil, ctxTransport(ctx))
		}
		transport, err = FlattenTransportWithError(merged)
		if err != nil {
			return Resolved{}, err
		}
	}
	return Resolved{
		APIURL:          apiURL,
		ContextName:     contextName,
		Environment:     env,
		Context:         ctx,
		Engine:          engine,
		Token:           token,
		Transport:       transport,
		TLSClientConfig: mtlsConfig,
	}, nil
}

func engineOverrideUsesStoredToken(engineOverride string, ctx *Context) bool {
	engineOverride = strings.TrimSpace(engineOverride)
	if engineOverride == "" {
		return false
	}
	if ctx == nil {
		return true
	}
	ctxEngine := strings.TrimSpace(ctx.Engine)
	if ctxEngine == "" {
		return true
	}
	return ctxEngine != engineOverride
}

func resolveToken(ctx *Context, env *Environment) (string, error) {
	if ctx != nil {
		if token, ok, err := TokenFromAuth(ctx.Auth); err != nil {
			return "", err
		} else if ok {
			return token, nil
		}
	}
	if env != nil && (ctx == nil || NormalizeAPIURL(ctx.APIURL) == NormalizeAPIURL(env.APIURL)) {
		if token, ok, err := TokenFromAuth(env.Auth); err != nil {
			return "", err
		} else if ok {
			return token, nil
		}
	}
	return "", nil
}

func resolveMTLSConfig(input ResolveInput, ctx *Context, env *Environment) (*tls.Config, bool, error) {
	certFile := firstNonEmpty(input.FlagMTLSCert, input.EnvMTLSCert)
	keyFile := firstNonEmpty(input.FlagMTLSKey, input.EnvMTLSKey)
	if certFile != "" || keyFile != "" {
		cfg, err := loadMTLSConfig("", "", certFile, keyFile)
		return cfg, false, err
	}
	if ctx != nil {
		if cfg, ok, err := MTLSConfigFromAuth(ctx.Auth); err != nil {
			return nil, false, err
		} else if ok {
			return cfg, true, nil
		}
	}
	if env != nil && (ctx == nil || NormalizeAPIURL(ctx.APIURL) == NormalizeAPIURL(env.APIURL)) {
		if cfg, ok, err := MTLSConfigFromAuth(env.Auth); err != nil {
			return nil, false, err
		} else if ok {
			return cfg, true, nil
		}
	}
	return nil, false, nil
}

func MTLSConfigFromAuth(auth *Auth) (*tls.Config, bool, error) {
	if auth == nil || auth.MTLS == nil {
		return nil, false, nil
	}
	if strings.TrimSpace(auth.MTLS.Certificate) == "" &&
		strings.TrimSpace(auth.MTLS.Key) == "" &&
		strings.TrimSpace(auth.MTLS.CertificateFile) == "" &&
		strings.TrimSpace(auth.MTLS.KeyFile) == "" &&
		auth.MTLS.Storage == nil {
		return nil, false, nil
	}
	if auth.MTLS.Storage != nil {
		if hasMTLSAlias(auth.MTLS) {
			return nil, false, errors.New("mTLS storage cannot be mixed with certificate/key aliases")
		}
		cfg, err := loadMTLSStorageConfig(auth.MTLS.Storage)
		if err != nil {
			return nil, false, err
		}
		return cfg, true, nil
	}
	cfg, err := loadMTLSConfig(
		auth.MTLS.Certificate,
		auth.MTLS.Key,
		auth.MTLS.CertificateFile,
		auth.MTLS.KeyFile,
	)
	if err != nil {
		return nil, false, err
	}
	return cfg, true, nil
}

func loadMTLSConfig(certPEM string, keyPEM string, certFile string, keyFile string) (*tls.Config, error) {
	certPEM = strings.TrimSpace(certPEM)
	keyPEM = strings.TrimSpace(keyPEM)
	certFile = strings.TrimSpace(certFile)
	keyFile = strings.TrimSpace(keyFile)
	hasInline := certPEM != "" || keyPEM != ""
	hasFiles := certFile != "" || keyFile != ""
	if hasInline && hasFiles {
		return nil, errors.New("mTLS certificate and key must be configured either inline or with files, not both")
	}
	var certificate tls.Certificate
	var err error
	switch {
	case hasInline:
		if certPEM == "" || keyPEM == "" {
			return nil, errors.New("mTLS inline certificate and key are both required")
		}
		certificate, err = tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	case hasFiles:
		if certFile == "" || keyFile == "" {
			return nil, errors.New("mTLS certificate and key files are both required")
		}
		certificate, err = tls.LoadX509KeyPair(certFile, keyFile)
	default:
		return nil, errors.New("mTLS certificate and key are required")
	}
	if err != nil {
		return nil, fmt.Errorf("load mTLS certificate: %w", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{certificate}}, nil
}

func hasMTLSAlias(mtls *MTLS) bool {
	if mtls == nil {
		return false
	}
	return strings.TrimSpace(mtls.Certificate) != "" ||
		strings.TrimSpace(mtls.Key) != "" ||
		strings.TrimSpace(mtls.CertificateFile) != "" ||
		strings.TrimSpace(mtls.KeyFile) != ""
}

func TokenFromAuth(auth *Auth) (string, bool, error) {
	if auth == nil || auth.Token == nil {
		return "", false, nil
	}
	if auth.Token.Storage == nil {
		return "", false, errors.New("token storage kind is required")
	}
	kind := strings.TrimSpace(auth.Token.Storage.Kind)
	if kind == "" {
		return "", false, errors.New("token storage kind is required")
	}
	switch kind {
	case TokenStorageInline:
		return auth.Token.Storage.Value, auth.Token.Storage.Value != "", nil
	case TokenStorageKeychain:
		return tokenFromKeychainStorage(auth.Token.Storage)
	default:
		return "", false, fmt.Errorf("token storage kind %q is not supported", kind)
	}
}

func envTransport(env *Environment) *TransportConfig {
	if env == nil {
		return nil
	}
	return env.Transport
}

func ctxTransport(ctx *Context) *TransportConfig {
	if ctx == nil {
		return nil
	}
	return ctx.Transport
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func isTokenExpired(token string, now time.Time) (bool, error) {
	claims, err := parseJWTClaims(token)
	if err != nil {
		return false, err
	}
	if claims == nil || claims.Exp == nil {
		return false, nil
	}
	exp, err := claims.Exp.Float64()
	if err != nil {
		return false, nil
	}
	return now.After(time.Unix(int64(exp), 0)), nil
}

type jwtClaims struct {
	Exp *json.Number `json:"exp"`
}

func parseJWTClaims(token string) (*jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, nil
	}
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, nil
	}
	return &claims, nil
}
