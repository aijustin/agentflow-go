package runstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/identity"
)

const blobDigestHexLength = sha256.Size * 2

// NewBlobRefForContext creates a content-addressed reference scoped to the
// authenticated tenant. Principal-less callers create legacy global digest
// IDs — writing unscoped data is always allowed; tenant-strict mode (the
// default) only fails closed when reading tenant-scoped data, never when
// creating new unowned content.
func NewBlobRefForContext(ctx context.Context, data []byte) (BlobRef, error) {
	ref := NewBlobRef("", data)
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		ref.ID = ref.Sha256
		return ref, nil
	}
	ref.ID = blobTenantHash(tenantID) + ref.Sha256
	return ref, nil
}

// AuthorizeBlobAccess rejects scoped references owned by another tenant.
// Legacy unscoped references are unprotected data and stay readable by
// anyone. Tenant-strict mode (the default) additionally rejects
// principal-less reads of tenant-scoped references;
// ContextWithTenantPermissive restores fail-open access for trusted
// maintenance callers.
func AuthorizeBlobAccess(ctx context.Context, ref BlobRef) error {
	tenantHash, _, legacy, err := ParseBlobID(ref.ID)
	if err != nil {
		return err
	}
	if legacy {
		return nil
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		if TenantStrictModeFromContext(ctx) {
			return ErrTenantRequired
		}
		return nil
	}
	if tenantHash != blobTenantHash(tenantID) {
		return ErrTenantMismatch
	}
	return nil
}

// ParseBlobID accepts legacy 64-hex content IDs and tenant-scoped 128-hex IDs.
// It returns the tenant hash (empty for legacy) and the content digest.
func ParseBlobID(id string) (tenantHash, digest string, legacy bool, err error) {
	switch len(id) {
	case blobDigestHexLength:
		if !validBlobHex(id) {
			return "", "", false, fmt.Errorf("runstate: invalid blob id %q", id)
		}
		return "", id, true, nil
	case 2 * blobDigestHexLength:
		if !validBlobHex(id) {
			return "", "", false, fmt.Errorf("runstate: invalid blob id %q", id)
		}
		return id[:blobDigestHexLength], id[blobDigestHexLength:], false, nil
	default:
		return "", "", false, fmt.Errorf("runstate: invalid blob id %q", id)
	}
}

// FilterBlobRefsForTenant keeps only references physically scoped to tenantID.
// Legacy references are deliberately excluded from tenant-local retention,
// because their owner cannot be proven.
func FilterBlobRefsForTenant(refs []BlobRef, tenantID string) []BlobRef {
	if tenantID == "" {
		return refs
	}
	expected := blobTenantHash(tenantID)
	out := make([]BlobRef, 0, len(refs))
	for _, ref := range refs {
		tenantHash, _, legacy, err := ParseBlobID(ref.ID)
		if err == nil && !legacy && tenantHash == expected {
			out = append(out, ref)
		}
	}
	return out
}

// FilterBlobRefsForContext applies the same access boundary to BlobAdmin.List
// as Get/Delete. A principal-less caller in tenant-strict mode (the default)
// sees only legacy unscoped references; ContextWithTenantPermissive restores
// the global admin view. Authenticated callers see their own tenant-scoped
// references plus legacy ones.
func FilterBlobRefsForContext(ctx context.Context, refs []BlobRef) ([]BlobRef, error) {
	if tenantIDFromContext(ctx) == "" && !TenantStrictModeFromContext(ctx) {
		return refs, nil
	}
	out := make([]BlobRef, 0, len(refs))
	for _, ref := range refs {
		if err := AuthorizeBlobAccess(ctx, ref); err == nil {
			out = append(out, ref)
		}
	}
	return out, nil
}

func tenantIDFromContext(ctx context.Context) string {
	principal, ok := identity.PrincipalFromContext(ctx)
	if !ok {
		return ""
	}
	return principal.Scope.TenantID
}

func blobTenantHash(tenantID string) string {
	sum := sha256.Sum256([]byte("agentflow/blob-tenant/v1\x00" + tenantID))
	return hex.EncodeToString(sum[:])
}

func validBlobHex(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && (len(decoded) == sha256.Size || len(decoded) == 2*sha256.Size)
}
