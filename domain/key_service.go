package domain

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/tamnd/githome/store"
)

var (
	// ErrInvalidSSHKey is returned when the provided public key cannot be parsed.
	ErrInvalidSSHKey = errors.New("domain: invalid SSH public key")
	// ErrDuplicateKey is returned when the fingerprint is already registered.
	ErrDuplicateKey = errors.New("domain: SSH key fingerprint already in use")
	// ErrNotFound is returned when a keyed lookup finds no matching row.
	ErrNotFound = errors.New("domain: not found")
)

// KeyStore is the narrow store interface the KeyService depends on.
type KeyStore interface {
	SSHKeysByUser(ctx context.Context, userPK int64) ([]*store.SSHKeyRow, error)
	DeployKeysByRepo(ctx context.Context, repoPK int64) ([]*store.SSHKeyRow, error)
	SSHKeyByPK(ctx context.Context, pk int64) (*store.SSHKeyRow, error)
	SSHKeyByDBID(ctx context.Context, dbID int64) (*store.SSHKeyRow, error)
	InsertSSHKey(ctx context.Context, k *store.SSHKeyRow) error
	DeleteSSHKey(ctx context.Context, pk int64) error
	BranchProtectionByPattern(ctx context.Context, repoPK int64, pattern string) (*store.BranchProtectionRow, error)
	UpsertBranchProtection(ctx context.Context, r *store.BranchProtectionRow) error
	DeleteBranchProtection(ctx context.Context, repoPK int64, pattern string) error
}

// KeyService manages SSH keys (user and deploy) and branch protection rules.
type KeyService struct {
	store KeyStore
}

// NewKeyService creates a KeyService over the store.
func NewKeyService(st KeyStore) *KeyService { return &KeyService{store: st} }

// ListDeployKeys returns all deploy keys for repoPK.
func (s *KeyService) ListDeployKeys(ctx context.Context, repoPK int64) ([]*store.SSHKeyRow, error) {
	return s.store.DeployKeysByRepo(ctx, repoPK)
}

// ListUserKeys returns all SSH keys for userPK (not deploy keys).
func (s *KeyService) ListUserKeys(ctx context.Context, userPK int64) ([]*store.SSHKeyRow, error) {
	return s.store.SSHKeysByUser(ctx, userPK)
}

// GetDeployKey returns the deploy key with the given DBID belonging to repoPK.
func (s *KeyService) GetDeployKey(ctx context.Context, repoPK, dbID int64) (*store.SSHKeyRow, error) {
	k, err := s.store.SSHKeyByDBID(ctx, dbID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if k.RepoPK == nil || *k.RepoPK != repoPK {
		return nil, ErrNotFound
	}
	return k, nil
}

// CreateDeployKey parses and registers a new deploy key on repoPK.
func (s *KeyService) CreateDeployKey(ctx context.Context, repoPK, ownerPK int64,
	title, rawKey string, readOnly bool) (*store.SSHKeyRow, error) {
	keyType, fp, err := parseSSHPublicKey(rawKey)
	if err != nil {
		return nil, ErrInvalidSSHKey
	}
	// Normalize to base public key part only (strip comment).
	normalized := normalizeKey(rawKey, keyType)
	k := &store.SSHKeyRow{
		UserPK:      ownerPK,
		KeyType:     keyType,
		PublicKey:   normalized,
		Fingerprint: fp,
		ReadOnly:    readOnly,
		RepoPK:      &repoPK,
	}
	if t := strings.TrimSpace(title); t != "" {
		k.Title = &t
	}
	if err := s.store.InsertSSHKey(ctx, k); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "unique") {
			return nil, ErrDuplicateKey
		}
		return nil, err
	}
	return k, nil
}

// DeleteDeployKey removes the deploy key identified by DBID from repoPK.
func (s *KeyService) DeleteDeployKey(ctx context.Context, repoPK, dbID int64) error {
	k, err := s.store.SSHKeyByDBID(ctx, dbID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if k.RepoPK == nil || *k.RepoPK != repoPK {
		return ErrNotFound
	}
	return s.store.DeleteSSHKey(ctx, k.PK)
}

// CreateUserKey parses and registers a new SSH key for userPK.
func (s *KeyService) CreateUserKey(ctx context.Context, userPK int64, title, rawKey string) (*store.SSHKeyRow, error) {
	keyType, fp, err := parseSSHPublicKey(rawKey)
	if err != nil {
		return nil, ErrInvalidSSHKey
	}
	normalized := normalizeKey(rawKey, keyType)
	k := &store.SSHKeyRow{
		UserPK:      userPK,
		KeyType:     keyType,
		PublicKey:   normalized,
		Fingerprint: fp,
	}
	if t := strings.TrimSpace(title); t != "" {
		k.Title = &t
	}
	if err := s.store.InsertSSHKey(ctx, k); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "unique") {
			return nil, ErrDuplicateKey
		}
		return nil, err
	}
	return k, nil
}

// DeleteUserKey removes the SSH key identified by DBID for userPK.
func (s *KeyService) DeleteUserKey(ctx context.Context, userPK, dbID int64) error {
	k, err := s.store.SSHKeyByDBID(ctx, dbID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if k.UserPK != userPK || k.RepoPK != nil {
		return ErrNotFound
	}
	return s.store.DeleteSSHKey(ctx, k.PK)
}

// GetBranchProtection returns the protection rule for a branch pattern.
func (s *KeyService) GetBranchProtection(ctx context.Context, repoPK int64, branch string) (*store.BranchProtectionRow, error) {
	r, err := s.store.BranchProtectionByPattern(ctx, repoPK, branch)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	return r, err
}

// SetBranchProtection creates or replaces a branch protection rule.
func (s *KeyService) SetBranchProtection(ctx context.Context, r *store.BranchProtectionRow) error {
	return s.store.UpsertBranchProtection(ctx, r)
}

// DeleteBranchProtection removes a branch protection rule.
func (s *KeyService) DeleteBranchProtection(ctx context.Context, repoPK int64, branch string) error {
	err := s.store.DeleteBranchProtection(ctx, repoPK, branch)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// normalizeKey returns "keytype base64encoded" without any comment field.
func normalizeKey(raw, keyType string) string {
	parts := strings.Fields(strings.TrimSpace(raw))
	if len(parts) >= 2 {
		return keyType + " " + parts[1]
	}
	return raw
}

// parseSSHPublicKey parses an SSH public key and returns (keyType, SHA256 fingerprint, error).
func parseSSHPublicKey(raw string) (keyType, fingerprint string, err error) {
	parts := strings.Fields(strings.TrimSpace(raw))
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid key format")
	}
	keyBytes, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("invalid key base64: %w", err)
	}
	pk, err := ssh.ParsePublicKey(keyBytes)
	if err != nil {
		return "", "", fmt.Errorf("invalid ssh key: %w", err)
	}
	sum := sha256.Sum256(pk.Marshal())
	fp := "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	return pk.Type(), fp, nil
}
