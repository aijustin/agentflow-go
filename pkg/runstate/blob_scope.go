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
// authenticated tenant. Principal-less non-strict callers retain the legacy
// global digest ID for backward compatibility and administrative workflows.
func NewBlobRefForContext(ctx context.Context, data []byte) (BlobRef, error) {
	ref := NewBlobRef("", data)
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		if TenantStrictModeFromContext(ctx) {
			return BlobRef{}, ErrTenantRequired
		}
		ref.ID = ref.Sha256
		return ref, nil
	}
	ref.ID = blobTenantHash(tenantID) + ref.Sha256
	return ref, nil
}

// AuthorizeBlobAccess rejects scoped references owned by another tenant and
// rejects legacy unscoped references in tenant-strict mode.
func AuthorizeBlobAccess(ctx context.Context, ref BlobRef) error {
	tenantHash, _, legacy, err := ParseBlobID(ref.ID)
	if err != nil {
		return err
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		if TenantStrictModeFromContext(ctx) {
			return ErrTenantRequired
		}
		return nil
	}
	if legacy {
		if TenantStrictModeFromContext(ctx) {
			return ErrTenantMismatch
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
// as Get/Delete. Principal-less non-strict callers retain the global admin view.
func FilterBlobRefsForContext(ctx context.Context, refs []BlobRef) ([]BlobRef, error) {
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		if TenantStrictModeFromContext(ctx) {
			return nil, ErrTenantRequired
		}
		return refs, nil
	}
	if TenantStrictModeFromContext(ctx) {
		return FilterBlobRefsForTenant(refs, tenantID), nil
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
