// See LICENSE file in the project root for license information.

package webtty

import (
	"errors"
	"strings"
	"time"
)

func (s *session) logSessionAccepted() {
	if s == nil || s.logger == nil || s.acceptedLogged {
		return
	}
	s.acceptedLogged = true
	if s.authenticatedAt.IsZero() {
		s.authenticatedAt = time.Now()
	}
	attrs := s.sessionAuditAttrs()
	attrs = appendSessionPolicyAttrs(attrs, s.cfg)
	s.logger.Info("session accepted", attrs...)
}

func (s *session) logSessionRejected(err error) {
	if s == nil || s.logger == nil {
		return
	}
	attrs := s.sessionAuditAttrs()
	attrs = append(attrs,
		"reason_code", webTTYSessionErrorReasonCode(err),
		"error", err,
	)
	attrs = appendSessionPolicyAttrs(attrs, s.cfg)
	s.logger.Warn("session rejected", attrs...)
}

func appendSessionPolicyAttrs(attrs []any, cfg *ServerConfig) []any {
	return append(attrs,
		"e2e", webTTYServerE2EConfigured(cfg),
		"client_proof_required", webTTYClientProofRequired(cfg),
		"execution_mode", executionModeLogValue(cfg),
	)
}

func (s *session) sessionAuditAttrs() []any {
	if s == nil {
		return nil
	}
	cfg := s.cfg
	if cfg == nil {
		cfg = &ServerConfig{}
	}
	if s.serverKeyID == "" && cfg.EndpointIdentity != nil {
		s.serverKeyID = EncodeE2EKeyMaterial(cfg.EndpointIdentity.Signing.KeyID)
	}
	var attrs []any
	appendStringAttr := func(key string, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			attrs = append(attrs, key, value)
		}
	}
	appendStringAttr("workspace_id", cfg.WorkspaceID)
	appendStringAttr("project_id", cfg.ProjectID)
	appendStringAttr("server_id", cfg.ServerID)
	appendStringAttr("server_signing_key_id", s.serverKeyID)
	appendStringAttr("client_principal_id", s.clientPrincipal)
	appendStringAttr("client_device_id", s.clientDeviceID)
	appendStringAttr("client_browser_id", s.clientBrowserID)
	appendStringAttr("client_signing_key_id", s.clientKeyID)
	return attrs
}

func webTTYClientProofRequired(cfg *ServerConfig) bool {
	return cfg != nil && cfg.RequireClientProof != nil && *cfg.RequireClientProof
}

func webTTYServerE2EConfigured(cfg *ServerConfig) bool {
	if cfg == nil {
		return false
	}
	if cfg.EndpointIdentity != nil || cfg.PayloadCryptoResolver != nil || cfg.PayloadCrypto != nil {
		return true
	}
	return cfg.RequireSessionKeyGrant != nil && *cfg.RequireSessionKeyGrant
}

func executionModeLogValue(cfg *ServerConfig) string {
	if cfg == nil || cfg.ExecutionMode == nil {
		return ""
	}
	return string(*cfg.ExecutionMode)
}

func webTTYSessionErrorReasonCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errWebTTYClientProofRequired):
		return "client_proof_required"
	case errors.Is(err, errWebTTYClientProofInvalid):
		return "client_proof_invalid"
	case errors.Is(err, errWebTTYClientProofUnauthorized):
		return "client_unauthorized"
	case errors.Is(err, errSessionOperationTimeout):
		return "operation_timeout"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "session key grant"):
		return "session_key_grant_invalid"
	case strings.Contains(msg, "e2e"):
		return "e2e_configuration_error"
	case strings.Contains(msg, "permission denied"):
		return "permission_denied"
	case strings.Contains(msg, "timeout"):
		return "operation_timeout"
	default:
		return "session_error"
	}
}
