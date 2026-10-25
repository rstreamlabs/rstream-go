// See LICENSE file in the project root for license information.

package webtty

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

// ManagedSessionConfig describes the Engine relay used by this connection.
// Fetch Capabilities from that Engine with Client.GetWebTTYCapabilities before
// opening the session. Capabilities do not grant authorization or control.
type ManagedSessionConfig struct {
	Capabilities rstream.WebTTYCapabilities
	// ExecutionSeconds opts a new non-interactive command into a bounded lifetime
	// independent of its first reader. Zero preserves the connection-bound lifetime.
	ExecutionSeconds int
}

func cloneManagedSessionConfig(value *ManagedSessionConfig) *ManagedSessionConfig {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Capabilities.ExecutionTransports = slices.Clone(value.Capabilities.ExecutionTransports)
	return &cloned
}

func prepareManagedSession(cfg *SessionConfig, endpoint *clientEndpoint) error {
	managed := cfg.Managed
	if managed == nil {
		return nil
	}
	if !managed.Capabilities.ManagedProtocol {
		return fmt.Errorf("engine does not support managed WebTTY")
	}
	if managed.ExecutionSeconds < 0 {
		return fmt.Errorf("WebTTY execution duration must be positive")
	}
	if managed.ExecutionSeconds == 0 {
		return nil
	}
	if cfg.Attach != nil || cfg.Interactive || cfg.AllocateTTY {
		return fmt.Errorf("detached WebTTY execution requires a new non-interactive command without a TTY")
	}
	if !managed.Capabilities.DetachedExecution {
		return fmt.Errorf("engine does not support detached WebTTY execution")
	}
	if managed.ExecutionSeconds > managed.Capabilities.MaxExecutionSeconds {
		return fmt.Errorf("WebTTY execution duration exceeds the Engine limit")
	}
	if (endpoint.Transport != WebTTYTransportWebSocket && endpoint.Transport != WebTTYTransportWebTransport) || !slices.Contains(managed.Capabilities.ExecutionTransports, rstream.WebTTYTransport(endpoint.Transport)) {
		return fmt.Errorf("engine does not support detached WebTTY execution over %s", endpoint.Transport)
	}
	parsed, err := url.Parse(endpoint.URL)
	if err != nil {
		return fmt.Errorf("invalid managed WebTTY endpoint")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return fmt.Errorf("invalid managed WebTTY query")
	}
	if _, supplied := query["execution_seconds"]; supplied {
		return fmt.Errorf("WebTTY execution duration must be supplied only through Managed.ExecutionSeconds")
	}
	if modes, supplied := query["session_mode"]; supplied && (len(modes) != 1 || modes[0] != "non-interactive") {
		return fmt.Errorf("detached WebTTY execution conflicts with the URL session mode")
	}
	query.Set("execution_seconds", strconv.Itoa(managed.ExecutionSeconds))
	query.Set("session_mode", "non-interactive")
	parsed.RawQuery = query.Encode()
	endpoint.URL = parsed.String()
	return nil
}

func (c *clientRuntime) acceptManagedAck(ack *pb.Ack) error {
	if c.cfg == nil {
		return nil
	}
	if c.cfg.Managed != nil || c.cfg.Attach != nil {
		c.canTerminate = c.canTerminate && c.cfg.Managed != nil && c.cfg.Managed.Capabilities.ExplicitTermination
	}
	if c.cfg.Attach != nil {
		c.managedSessionID = strings.TrimSpace(c.cfg.Attach.SessionID)
		if ack.GetManagedSessionId() != "" && ack.GetManagedSessionId() != c.managedSessionID {
			return fmt.Errorf("WebTTY Ack does not identify the attached session")
		}
		return nil
	}
	if c.cfg.Managed == nil || c.cfg.Managed.ExecutionSeconds == 0 {
		return nil
	}
	identity := ack.GetManagedSessionId()
	if identity == "" || len(identity) > 256 || identity != strings.TrimSpace(identity) {
		return fmt.Errorf("engine did not confirm the detached WebTTY session identity")
	}
	c.managedSessionID = identity
	return nil
}
