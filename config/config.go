// See LICENSE file in the project root for license information.

package config

import (
	"fmt"
	"strings"
)

type Config struct {
	Version      int           `yaml:"version,omitempty"`
	Defaults     Defaults      `yaml:"defaults,omitempty"`
	Environments []Environment `yaml:"environments,omitempty"`
	Contexts     []Context     `yaml:"contexts,omitempty"`
}

type Defaults struct {
	Context *DefaultContext `yaml:"context,omitempty"`
}

type DefaultContext struct {
	Name string `yaml:"name,omitempty"`
}

type Environment struct {
	APIURL    string           `yaml:"apiUrl"`
	Auth      *Auth            `yaml:"auth,omitempty"`
	Transport *TransportConfig `yaml:"transport,omitempty"`
}

type Context struct {
	Name            string           `yaml:"name"`
	APIURL          string           `yaml:"apiUrl,omitempty"`
	ProjectEndpoint string           `yaml:"projectEndpoint,omitempty"`
	Engine          string           `yaml:"engine,omitempty"`
	TURNDomain      string           `yaml:"turnDomain,omitempty"`
	TURNPort        int              `yaml:"turnPort,omitempty"`
	TURNSPort       int              `yaml:"turnsPort,omitempty"`
	Auth            *Auth            `yaml:"auth,omitempty"`
	Transport       *TransportConfig `yaml:"transport,omitempty"`
}

type Auth struct {
	Token *Token `yaml:"token,omitempty"`
	MTLS  *MTLS  `yaml:"mtls,omitempty"`
}

type Token struct {
	Storage *TokenStorage `yaml:"storage,omitempty"`
}

type TokenStorage struct {
	Kind     string `yaml:"kind,omitempty"`
	Provider string `yaml:"provider,omitempty"`
	Value    string `yaml:"value,omitempty"`
	Service  string `yaml:"service,omitempty"`
	Account  string `yaml:"account,omitempty"`
}

type MTLS struct {
	Certificate     string       `yaml:"certificate,omitempty"`
	CertificateFile string       `yaml:"certificateFile,omitempty"`
	Key             string       `yaml:"key,omitempty"`
	KeyFile         string       `yaml:"keyFile,omitempty"`
	Storage         *MTLSStorage `yaml:"storage,omitempty"`
}

type MTLSStorage struct {
	Kind              string `yaml:"kind,omitempty"`
	Provider          string `yaml:"provider,omitempty"`
	Module            string `yaml:"module,omitempty"`
	OpenSSLProvider   string `yaml:"opensslProvider,omitempty"`
	TokenLabel        string `yaml:"tokenLabel,omitempty"`
	TokenSerial       string `yaml:"tokenSerial,omitempty"`
	Slot              *int   `yaml:"slot,omitempty"`
	KeyLabel          string `yaml:"keyLabel,omitempty"`
	KeyIDHex          string `yaml:"keyIdHex,omitempty"`
	Certificate       string `yaml:"certificate,omitempty"`
	CertificateFile   string `yaml:"certificateFile,omitempty"`
	CertificateLabel  string `yaml:"certificateLabel,omitempty"`
	CertificateIDHex  string `yaml:"certificateIdHex,omitempty"`
	CertificateSHA256 string `yaml:"certificateSHA256,omitempty"`
	PINEnv            string `yaml:"pinEnv,omitempty"`
	MaxSessions       int    `yaml:"maxSessions,omitempty"`
}

func (c *Config) EnsureVersion() {
	if c.Version == 0 {
		c.Version = 1
	}
}

func (c *Config) Normalize() {
	c.EnsureVersion()
	for i := range c.Environments {
		c.Environments[i].APIURL = NormalizeAPIURL(c.Environments[i].APIURL)
	}
	for i := range c.Contexts {
		c.Contexts[i].APIURL = NormalizeAPIURL(c.Contexts[i].APIURL)
	}
	if c.Defaults.Context == nil {
		return
	}
	if strings.TrimSpace(c.Defaults.Context.Name) == "" {
		c.Defaults.Context = nil
	}
}

func (c *Config) FindEnvironment(apiURL string) (*Environment, int) {
	apiURL = NormalizeAPIURL(apiURL)
	for i := range c.Environments {
		if NormalizeAPIURL(c.Environments[i].APIURL) == apiURL {
			return &c.Environments[i], i
		}
	}
	return nil, -1
}

func (c *Config) EnsureEnvironment(apiURL string) *Environment {
	apiURL = NormalizeAPIURL(apiURL)
	if env, _ := c.FindEnvironment(apiURL); env != nil {
		env.APIURL = apiURL
		return env
	}
	c.Environments = append(c.Environments, Environment{APIURL: apiURL})
	return &c.Environments[len(c.Environments)-1]
}

func (c *Config) FindContextByName(name string) (*Context, int, error) {
	matches := c.contextMatches(name)
	if len(matches) == 0 {
		return nil, -1, nil
	}
	if len(matches) > 1 {
		return nil, -1, contextAmbiguousError(name)
	}
	idx := matches[0]
	return &c.Contexts[idx], idx, nil
}

func (c *Config) FindContextByNameAndAPIURL(name, apiURL string) (*Context, int, error) {
	apiURL = NormalizeAPIURL(apiURL)
	matches := c.contextMatches(name)
	if len(matches) == 0 {
		return nil, -1, nil
	}
	var exact []int
	for _, idx := range matches {
		if NormalizeAPIURL(c.Contexts[idx].APIURL) == apiURL {
			exact = append(exact, idx)
		}
	}
	switch len(exact) {
	case 1:
		return &c.Contexts[exact[0]], exact[0], nil
	case 0:
		return nil, -1, contextNotFoundForAPIURLError(name, apiURL, matches, c)
	default:
		return nil, -1, contextDuplicateError(name, apiURL)
	}
}

func (c *Config) FindContextForAPIURL(name, apiURL string) (*Context, int, error) {
	apiURL = NormalizeAPIURL(apiURL)
	matches := c.contextMatches(name)
	if len(matches) == 0 {
		return nil, -1, nil
	}
	var exact []int
	for _, idx := range matches {
		if NormalizeAPIURL(c.Contexts[idx].APIURL) == apiURL {
			exact = append(exact, idx)
		}
	}
	switch len(exact) {
	case 1:
		return &c.Contexts[exact[0]], exact[0], nil
	case 0:
		// fall through to unlinked selection
	default:
		return nil, -1, contextDuplicateError(name, apiURL)
	}
	var unlinked []int
	for _, idx := range matches {
		if c.Contexts[idx].APIURL == "" {
			unlinked = append(unlinked, idx)
		}
	}
	switch len(unlinked) {
	case 1:
		if len(matches) > 1 {
			return nil, -1, contextAmbiguousError(name)
		}
		return &c.Contexts[unlinked[0]], unlinked[0], nil
	case 0:
		return nil, -1, contextNotFoundForAPIURLError(name, apiURL, matches, c)
	default:
		return nil, -1, contextAmbiguousError(name)
	}
}

func (c *Config) FindContextUnlinked(name string) (*Context, int, error) {
	matches := c.contextMatches(name)
	if len(matches) == 0 {
		return nil, -1, nil
	}
	var unlinked []int
	for _, idx := range matches {
		if c.Contexts[idx].APIURL == "" {
			unlinked = append(unlinked, idx)
		}
	}
	switch len(unlinked) {
	case 1:
		return &c.Contexts[unlinked[0]], unlinked[0], nil
	case 0:
		return nil, -1, contextNotFoundForAPIURLError(name, "", matches, c)
	default:
		return nil, -1, contextAmbiguousError(name)
	}
}

func NormalizeAPIURL(apiURL string) string {
	apiURL = strings.TrimSpace(apiURL)
	for strings.HasSuffix(apiURL, "/") && !strings.HasSuffix(apiURL, "://") {
		apiURL = strings.TrimSuffix(apiURL, "/")
	}
	return apiURL
}

func (c *Config) contextMatches(name string) []int {
	var matches []int
	for i := range c.Contexts {
		if c.Contexts[i].Name == name {
			matches = append(matches, i)
		}
	}
	return matches
}

func contextAmbiguousError(name string) error {
	return fmt.Errorf("context %q is ambiguous; specify --api-url or set RSTREAM_API_URL", name)
}

func contextDuplicateError(name, apiURL string) error {
	return fmt.Errorf("multiple contexts named %q exist for API URL %q", name, apiURL)
}

func contextNotFoundForAPIURLError(name, apiURL string, matches []int, cfg *Config) error {
	if len(matches) == 0 {
		return nil
	}
	for _, idx := range matches {
		if cfg.Contexts[idx].APIURL != "" && apiURL != "" {
			return fmt.Errorf("context %q not found for API URL %q", name, apiURL)
		}
	}
	if apiURL == "" {
		return fmt.Errorf("context %q not found", name)
	}
	return fmt.Errorf("context %q not found for API URL %q", name, apiURL)
}
