// See LICENSE file in the project root for license information.

package config

type Config struct {
	Version      int           `yaml:"version,omitempty"`
	Defaults     Defaults      `yaml:"defaults,omitempty"`
	Environments []Environment `yaml:"environments,omitempty"`
}

type Defaults struct {
	APIURL  string          `yaml:"apiUrl,omitempty"`
	Context *DefaultContext `yaml:"context,omitempty"`
}

type DefaultContext struct {
	APIURL string `yaml:"apiUrl,omitempty"`
	Name   string `yaml:"name,omitempty"`
}

type Environment struct {
	APIURL    string           `yaml:"apiUrl"`
	Auth      *Auth            `yaml:"auth,omitempty"`
	Transport *TransportConfig `yaml:"transport,omitempty"`
	Contexts  []Context        `yaml:"contexts,omitempty"`
}

type Context struct {
	Name            string           `yaml:"name"`
	ProjectEndpoint string           `yaml:"projectEndpoint,omitempty"`
	Engine          string           `yaml:"engine,omitempty"`
	Auth            *Auth            `yaml:"auth,omitempty"`
	Transport       *TransportConfig `yaml:"transport,omitempty"`
}

type Auth struct {
	Token *Token `yaml:"token,omitempty"`
}

type Token struct {
	Storage *TokenStorage `yaml:"storage,omitempty"`
}

type TokenStorage struct {
	Kind  string `yaml:"kind,omitempty"`
	Value string `yaml:"value,omitempty"`
}

func (c *Config) EnsureVersion() {
	if c.Version == 0 {
		c.Version = 1
	}
}

func (c *Config) FindEnvironment(apiURL string) (*Environment, int) {
	for i := range c.Environments {
		if c.Environments[i].APIURL == apiURL {
			return &c.Environments[i], i
		}
	}
	return nil, -1
}

func (c *Config) EnsureEnvironment(apiURL string) *Environment {
	if env, _ := c.FindEnvironment(apiURL); env != nil {
		return env
	}
	c.Environments = append(c.Environments, Environment{APIURL: apiURL})
	return &c.Environments[len(c.Environments)-1]
}

func (e *Environment) FindContext(name string) (*Context, int) {
	for i := range e.Contexts {
		if e.Contexts[i].Name == name {
			return &e.Contexts[i], i
		}
	}
	return nil, -1
}
