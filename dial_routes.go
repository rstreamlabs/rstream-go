// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

const (
	dialFallbackDelay  = 250 * time.Millisecond
	defaultDialTimeout = 30 * time.Second
)

func dialSetupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, defaultDialTimeout)
}

type dialRoute struct {
	target string
	proxy  string
}

func (r dialRoute) endpoint() string {
	if r.proxy != "" {
		return r.proxy
	}
	return r.target
}

type dialRoutes struct {
	targets []string
	proxies []string
}

func (r dialRoutes) count() int { return len(r.targets) * len(r.proxies) }

func (r dialRoutes) at(index int) dialRoute {
	proxy := index % len(r.proxies)
	target := (index/len(r.proxies) + proxy) % len(r.targets)
	return dialRoute{target: r.targets[target], proxy: r.proxies[proxy]}
}

func prepareDialRoutes(ctx context.Context, addr, proxyHTTP, proxySOCKS5 string, cfg dnsResolverConfig) (dialRoutes, error) {
	if proxyHTTP != "" && proxySOCKS5 != "" {
		return dialRoutes{}, errors.New("only one proxy transport can be configured")
	}
	proxy := ""
	if proxyHTTP != "" {
		u, err := url.Parse(proxyHTTP)
		if err != nil {
			return dialRoutes{}, fmt.Errorf("failed to parse HTTP proxy URL: %w", err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return dialRoutes{}, fmt.Errorf("unsupported HTTP proxy scheme %q", u.Scheme)
		}
		proxy, err = httpProxyDialAddress(u)
		if err != nil {
			return dialRoutes{}, err
		}
	} else if proxySOCKS5 != "" {
		p, err := socks5ProxyConfigFromRaw(proxySOCKS5, nil, nil)
		if err != nil {
			return dialRoutes{}, err
		}
		proxy = p.address
	}
	routes := dialRoutes{targets: []string{addr}, proxies: []string{""}}
	var err error
	if proxy == "" || cfg.enabled() {
		routes.targets, err = resolveDialAddresses(ctx, addr, cfg)
		if err != nil {
			return dialRoutes{}, err
		}
	}
	if proxy != "" {
		routes.proxies, err = resolveDialAddresses(ctx, proxy, cfg)
		if err != nil {
			return dialRoutes{}, err
		}
	}
	return routes, nil
}

func dialNetwork(base string, route dialRoute) string {
	host, _, err := net.SplitHostPort(route.endpoint())
	if err != nil {
		return base
	}
	if strings.Contains(host, ":") {
		return base + "6"
	}
	return base + "4"
}

type dialResult[T any] struct {
	value T
	err   error
}

// A route owns the complete handshake, including proxy and target TLS. At most
// two routes run concurrently. Cancel and join every loser before returning.
func raceDialRoutes[T any](ctx context.Context, routes dialRoutes, dial func(context.Context, dialRoute) (T, error), closeValue func(T)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan dialResult[T], 2)
	next, active := 0, 0
	start := func() {
		index := next
		next++
		active++
		budget := defaultDialTimeout
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			budget = max(2*time.Second, remaining/time.Duration((routes.count()-index+1)/2))
			budget = min(budget, remaining)
		}
		go func() {
			attemptCtx, stop := context.WithTimeout(ctx, budget)
			defer stop()
			value, err := dial(attemptCtx, routes.at(index))
			results <- dialResult[T]{value: value, err: err}
		}()
	}
	start()
	timer := time.NewTimer(dialFallbackDelay)
	defer timer.Stop()
	drain := func() {
		cancel()
		for active > 0 {
			result := <-results
			active--
			if result.err == nil {
				closeValue(result.value)
			}
		}
	}
	var firstErr error
	for active > 0 {
		select {
		case <-ctx.Done():
			drain()
			return zero, ctx.Err()
		case <-timer.C:
			if next < routes.count() && active < 2 {
				start()
			}
		case result := <-results:
			active--
			if result.err == nil {
				drain()
				if err := parent.Err(); err != nil {
					closeValue(result.value)
					return zero, err
				}
				return result.value, nil
			}
			if firstErr == nil {
				firstErr = result.err
			}
			if next < routes.count() {
				start()
				if active < 2 {
					timer.Reset(dialFallbackDelay)
				}
			}
		}
	}
	if err := parent.Err(); err != nil {
		return zero, err
	}
	return zero, firstErr
}
