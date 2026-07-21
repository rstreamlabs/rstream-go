// See LICENSE file in the project root for license information.

package config

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
)

type TURNCredentialsEnvOptions struct {
	ConfigPath      string
	APIURL          string
	Context         string
	Token           string
	ProjectID       string
	ProjectEndpoint string
	ClusterDomain   string
	TURNPort        int
	TURNSPort       int
	TTL             time.Duration
	Mode            *rstream.TURNCredentialMode
}

func CreateTURNCredentialsFromEnv(
	ctx context.Context,
	opts ...TURNCredentialsEnvOptions,
) (*rstream.TURNCredentials, error) {
	if len(opts) > 1 {
		return nil, errors.New("at most one TURN credentials options value is allowed")
	}
	var resolvedOpts TURNCredentialsEnvOptions
	if len(opts) == 1 {
		resolvedOpts = opts[0]
	}
	resolvedMode := resolvedOpts.Mode
	resolution, err := ResolveFromEnv(ClientEnvOptions{
		ConfigPath:   resolvedOpts.ConfigPath,
		APIURL:       resolvedOpts.APIURL,
		Context:      resolvedOpts.Context,
		Token:        resolvedOpts.Token,
		RequireToken: true,
	})
	if err != nil {
		return nil, err
	}
	projectEndpoint := strings.TrimSpace(resolvedOpts.ProjectEndpoint)
	clusterDomain := strings.TrimSpace(resolvedOpts.ClusterDomain)
	turnPort := resolvedOpts.TURNPort
	turnsPort := resolvedOpts.TURNSPort
	if resolution.Resolved.Context != nil {
		configured := resolution.Resolved.Context
		if projectEndpoint == "" {
			projectEndpoint = strings.TrimSpace(configured.ProjectEndpoint)
		}
		if projectEndpoint != "" && projectEndpoint == strings.TrimSpace(configured.ProjectEndpoint) {
			if clusterDomain == "" {
				clusterDomain = strings.TrimSpace(configured.TURNDomain)
			}
			if turnPort == 0 {
				turnPort = configured.TURNPort
			}
			if turnsPort == 0 {
				turnsPort = configured.TURNSPort
			}
		}
	}
	if clusterDomain == "" && projectEndpoint != "" && resolution.Resolved.Engine != "" {
		clusterDomain, err = clusterDomainFromEngine(projectEndpoint, resolution.Resolved.Engine)
		if err != nil && resolvedOpts.Mode != nil && *resolvedOpts.Mode == rstream.TURNCredentialModePAT {
			return nil, err
		}
	}
	if resolvedMode == nil && (clusterDomain == "" || turnPort == 0 || turnsPort == 0) {
		apiMode := rstream.TURNCredentialModeAPI
		resolvedMode = &apiMode
	}
	return rstream.CreateTURNCredentials(ctx, rstream.CreateTURNCredentialsOptions{
		APIURL:              resolution.Resolved.APIURL,
		Token:               resolution.Resolved.Token,
		ProjectID:           strings.TrimSpace(resolvedOpts.ProjectID),
		ProjectEndpoint:     projectEndpoint,
		ClusterDomain:       clusterDomain,
		TURNPort:            turnPort,
		TURNSPort:           turnsPort,
		TTL:                 resolvedOpts.TTL,
		Mode:                resolvedMode,
		ControlPlaneHeaders: resolution.Resolved.ControlPlaneHeaders,
	})
}

func clusterDomainFromEngine(projectEndpoint, engine string) (string, error) {
	host, _, err := net.SplitHostPort(strings.TrimSpace(engine))
	if err != nil {
		return "", errors.New("failed to derive the cluster domain from the engine address")
	}
	prefix := strings.TrimSpace(projectEndpoint) + "."
	if !strings.HasPrefix(host, prefix) {
		return "", errors.New("failed to derive the cluster domain from the engine address")
	}
	clusterDomain := strings.TrimPrefix(host, prefix)
	if clusterDomain == "" {
		return "", errors.New("failed to derive the cluster domain from the engine address")
	}
	return clusterDomain, nil
}
