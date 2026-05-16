package apikey

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

type StaticAuthenticator struct {
	principals map[[32]byte]identity.Principal
}

func NewStaticAuthenticator(keys map[string]identity.Principal) (*StaticAuthenticator, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("apikey auth: at least one key is required")
	}
	principals := make(map[[32]byte]identity.Principal, len(keys))
	for key, principal := range keys {
		if key == "" {
			return nil, fmt.Errorf("apikey auth: key is required")
		}
		if err := principal.Validate(); err != nil {
			return nil, fmt.Errorf("apikey auth: invalid principal for key: %w", err)
		}
		principals[sha256.Sum256([]byte(key))] = clonePrincipal(principal)
	}
	return &StaticAuthenticator{principals: principals}, nil
}

func (auth *StaticAuthenticator) AuthenticateAPIKey(ctx context.Context, key string) (identity.Principal, bool, error) {
	if err := ctx.Err(); err != nil {
		return identity.Principal{}, false, err
	}
	if key == "" {
		return identity.Principal{}, false, nil
	}
	principal, ok := auth.principals[sha256.Sum256([]byte(key))]
	if !ok {
		return identity.Principal{}, false, nil
	}
	return clonePrincipal(principal), true, nil
}

func clonePrincipal(principal identity.Principal) identity.Principal {
	if principal.Roles != nil {
		roles := make([]identity.Role, len(principal.Roles))
		copy(roles, principal.Roles)
		principal.Roles = roles
	}
	if principal.Metadata != nil {
		metadata := make(map[string]string, len(principal.Metadata))
		for key, value := range principal.Metadata {
			metadata[key] = value
		}
		principal.Metadata = metadata
	}
	return principal
}
