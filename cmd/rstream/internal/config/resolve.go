// See LICENSE file in the project root for license information.

package config

import (
	"errors"
	"fmt"

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
	apiURL := firstNonEmpty(input.FlagAPIURL, input.EnvAPIURL, cfg.Defaults.APIURL, defaultAPIURL)
	contextName := firstNonEmpty(input.FlagContext, input.EnvContext)
	if contextName == "" && cfg.Defaults.Context != nil && cfg.Defaults.Context.APIURL == apiURL {
		contextName = cfg.Defaults.Context.Name
	}
	env, _ := cfg.FindEnvironment(apiURL)
	var ctx *Context
	if contextName != "" {
		if env == nil {
			return Resolved{}, fmt.Errorf("no environment found for apiUrl %q", apiURL)
		}
		ctx, _ = env.FindContext(contextName)
		if ctx == nil {
			return Resolved{}, fmt.Errorf("context %q not found for apiUrl %q", contextName, apiURL)
		}
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
	if input.RequireEngine && engine == "" {
		return Resolved{}, errors.New("engine is required but not configured")
	}
	if input.RequireToken && token == "" {
		return Resolved{}, errors.New("token is required but not configured")
	}
	var transport *rstream.Transport
	if env != nil || ctx != nil {
		merged := MergeTransport(envTransport(env), ctxTransport(ctx))
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
		if token, ok, err := tokenFromAuth(ctx.Auth); err != nil {
			return "", err
		} else if ok {
			return token, nil
		}
	}
	if env != nil {
		if token, ok, err := tokenFromAuth(env.Auth); err != nil {
			return "", err
		} else if ok {
			return token, nil
		}
	}
	return "", nil
}

func tokenFromAuth(auth *Auth) (string, bool, error) {
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
