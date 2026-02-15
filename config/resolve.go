// See LICENSE file in the project root for license information.

package config

import (
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
	TokenStorageInline   = "inline"
	TokenStorageKeychain = "keychain"
)

type ResolveInput struct {
	Config        Config
	FlagAPIURL    string
	FlagContext   string
	FlagEngine    string
	FlagToken     string
	EnvAPIURL     string
	EnvContext    string
	EnvEngine     string
	EnvToken      string
	RequireToken  bool
	RequireEngine bool
	ResolveToken  bool
}

type Resolved struct {
	APIURL      string
	ContextName string
	Environment *Environment
	Context     *Context
	Engine      string
	Token       string
	Transport   *rstream.Transport
}

func Resolve(input ResolveInput) (Resolved, error) {
	cfg := input.Config
	apiURLExplicit := firstNonEmpty(input.FlagAPIURL, input.EnvAPIURL)
	contextName := firstNonEmpty(input.FlagContext, input.EnvContext)
	if contextName == "" && cfg.Defaults.Context != nil {
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
				return Resolved{}, fmt.Errorf("context %q not found for apiUrl %q", contextName, apiURLExplicit)
			}
			return Resolved{}, fmt.Errorf("context %q not found", contextName)
		}
		if apiURLExplicit != "" && ctx.APIURL != "" && ctx.APIURL != apiURLExplicit {
			return Resolved{}, fmt.Errorf("context %q belongs to apiUrl %q (selected apiUrl %q)", contextName, ctx.APIURL, apiURLExplicit)
		}
		if apiURLExplicit == "" && ctx.APIURL != "" {
			apiURLExplicit = ctx.APIURL
		}
	}
	apiURL := apiURLExplicit
	if apiURL == "" {
		apiURL = defaultAPIURL
	}
	env, _ := cfg.FindEnvironment(apiURL)
	if ctx != nil && ctx.APIURL == "" {
		env = nil
	}
	engine := firstNonEmpty(input.FlagEngine, input.EnvEngine)
	if engine == "" && ctx != nil {
		engine = ctx.Engine
	}
	token := ""
	shouldResolveToken := input.ResolveToken || input.RequireToken || input.FlagToken != "" || input.EnvToken != ""
	if shouldResolveToken {
		token = firstNonEmpty(input.FlagToken, input.EnvToken)
		if token == "" {
			var err error
			token, err = resolveToken(ctx, env)
			if err != nil {
				return Resolved{}, err
			}
		}
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
	if input.RequireToken && token == "" {
		return Resolved{}, errors.New("token is required but not configured (run rstream login or set RSTREAM_AUTHENTICATION_TOKEN)")
	}
	var transport *rstream.Transport
	if ctx != nil {
		var merged *TransportConfig
		if env != nil && ctx.APIURL != "" && ctx.APIURL == env.APIURL {
			merged = MergeTransport(envTransport(env), ctxTransport(ctx))
		} else {
			merged = MergeTransport(nil, ctxTransport(ctx))
		}
		transport = FlattenTransport(merged)
	}
	return Resolved{
		APIURL:      apiURL,
		ContextName: contextName,
		Environment: env,
		Context:     ctx,
		Engine:      engine,
		Token:       token,
		Transport:   transport,
	}, nil
}

func resolveToken(ctx *Context, env *Environment) (string, error) {
	if ctx != nil {
		if token, ok, err := TokenFromAuth(ctx.Auth); err != nil {
			return "", err
		} else if ok {
			return token, nil
		}
	}
	if env != nil && (ctx == nil || ctx.APIURL == env.APIURL) {
		if token, ok, err := TokenFromAuth(env.Auth); err != nil {
			return "", err
		} else if ok {
			return token, nil
		}
	}
	return "", nil
}

func TokenFromAuth(auth *Auth) (string, bool, error) {
	if auth == nil || auth.Token == nil {
		return "", false, nil
	}
	if auth.Token.Storage == nil {
		return "", false, errors.New("token storage kind is required")
	}
	kind := auth.Token.Storage.Kind
	if kind == "" {
		return "", false, errors.New("token storage kind is required")
	}
	switch kind {
	case TokenStorageInline:
		return auth.Token.Storage.Value, auth.Token.Storage.Value != "", nil
	case TokenStorageKeychain:
		return "", false, fmt.Errorf("token storage kind %q is not supported yet", kind)
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
