// See LICENSE file in the project root for license information.

package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CredentialOwner identifies one user or one workspace. Their IDs have separate
// namespaces; a workspace never supplies a human user ID.
type CredentialOwner struct {
	Kind        string `json:"kind"`
	UserID      string `json:"userId,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
}

func validIdentityReference(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}

func (o *CredentialOwner) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var owner CredentialOwner
	if err := json.Unmarshal(fields["kind"], &owner.Kind); err != nil {
		return errors.New("invalid credential owner kind")
	}
	if len(fields) != 2 {
		return errors.New("credential owner requires exactly one identity reference")
	}
	switch owner.Kind {
	case "user":
		if err := json.Unmarshal(fields["userId"], &owner.UserID); err != nil || !validIdentityReference(owner.UserID) {
			return errors.New("invalid user credential owner")
		}
	case "workspace":
		if err := json.Unmarshal(fields["workspaceId"], &owner.WorkspaceID); err != nil || !validIdentityReference(owner.WorkspaceID) {
			return errors.New("invalid workspace credential owner")
		}
	default:
		return errors.New("invalid credential owner kind")
	}
	*o = owner
	return nil
}

// CallerIdentity is the effective authority reported by the authenticated
// server. Nil Permissions means unspecified personal authority, while an empty
// pointed-to slice means no permissions. Workspace authority is always explicit.
type CallerIdentity struct {
	Owner         CredentialOwner `json:"owner"`
	CredentialID  string          `json:"credentialId,omitempty"`
	Name          string          `json:"name,omitempty"`
	Role          *string         `json:"role,omitempty"`
	Email         string          `json:"email,omitempty"`
	Permissions   *[]string       `json:"permissions,omitempty"`
	Resources     json.RawMessage `json:"resources,omitempty"`
	AuthExpiresAt string          `json:"authExpiresAt,omitempty"`
}

func (i *CallerIdentity) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return errors.New("caller identity must be an object")
	}
	if raw, present := fields["permissions"]; present {
		var entries []*string
		if err := json.Unmarshal(raw, &entries); err != nil {
			return err
		}
		for _, permission := range entries {
			if permission == nil {
				return errors.New("caller permissions must be strings")
			}
		}
	}
	if _, explicitOwner := fields["owner"]; !explicitOwner {
		identity, err := legacyCallerIdentity(fields)
		if err != nil {
			return err
		}
		*i = identity
		return nil
	}
	for key, value := range fields {
		switch key {
		case "owner", "credentialId", "name", "role", "email", "permissions", "resources", "authExpiresAt":
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return fmt.Errorf("caller identity field %s cannot be null", key)
			}
		default:
			return fmt.Errorf("unknown caller identity field %s", key)
		}
	}
	type identityJSON CallerIdentity
	var identity identityJSON
	if err := json.Unmarshal(data, &identity); err != nil {
		return err
	}
	if _, present := fields["credentialId"]; present && !validIdentityReference(identity.CredentialID) {
		return errors.New("invalid caller credential reference")
	}
	if identity.Owner.Kind == "workspace" {
		if identity.CredentialID == "" || identity.Permissions == nil {
			return errors.New("workspace identity requires a credential reference and explicit permissions")
		}
		if _, present := fields["role"]; present {
			return errors.New("workspace identity cannot have a human role")
		}
		if _, present := fields["email"]; present {
			return errors.New("workspace identity cannot have a human email")
		}
	} else if _, present := fields["role"]; !present {
		return errors.New("personal identity requires a role")
	}
	if len(identity.Resources) > 0 && bytes.TrimSpace(identity.Resources)[0] != '{' {
		return errors.New("caller resource limits must be an object")
	}
	if identity.AuthExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, identity.AuthExpiresAt); err != nil {
			return errors.New("invalid caller authentication deadline")
		}
	} else if _, present := fields["authExpiresAt"]; present {
		return errors.New("invalid caller authentication deadline")
	}
	*i = CallerIdentity(identity)
	return nil
}

func legacyCallerIdentity(fields map[string]json.RawMessage) (CallerIdentity, error) {
	known := make(map[string]json.RawMessage)
	for _, key := range []string{"id", "role", "name", "email", "permissions"} {
		if value, present := fields[key]; present {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return CallerIdentity{}, fmt.Errorf("legacy identity field %s cannot be null", key)
			}
			known[key] = value
		}
	}
	if _, present := known["role"]; !present {
		return CallerIdentity{}, errors.New("legacy identity requires a role")
	}
	data, err := json.Marshal(known)
	if err != nil {
		return CallerIdentity{}, err
	}
	var legacy Whoami
	if err := json.Unmarshal(data, &legacy); err != nil {
		return CallerIdentity{}, err
	}
	if !validIdentityReference(legacy.ID) {
		return CallerIdentity{}, errors.New("legacy identity requires a user reference")
	}
	identity := CallerIdentity{
		Owner: CredentialOwner{Kind: "user", UserID: legacy.ID},
		Name:  legacy.Name,
		Role:  &legacy.Role,
		Email: legacy.Email,
	}
	if legacy.Permissions != nil {
		identity.Permissions = &legacy.Permissions
	}
	return identity, nil
}

// Identity requests the owner-aware format. Older servers may ignore the query
// parameter and return a legacy personal response, which is normalized here.
// An explicitly malformed owner never falls back to the legacy user contract.
func (c *Client) Identity(ctx context.Context) (CallerIdentity, error) {
	var out CallerIdentity
	_, err := c.doJSON(ctx, http.MethodGet, "/api/whoami", url.Values{"format": {"identity"}}, &out)
	return out, err
}
