package core

import (
	"context"
	"strings"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
)

// keyManager resolves the account's adapter and checks the keys.manage capability.
func (c *Core) keyManager(ctx context.Context, accountID string) (adapter.KeyManager, error) {
	row, ad, err := c.adapterFor(ctx, accountID)
	if err != nil {
		return nil, err
	}
	km, ok := ad.(adapter.KeyManager)
	if !ok || !ad.Info().Has(adapter.CapKeys) {
		return nil, errUnsupported(adapter.CapKeys) // capability first: a WhatsApp account never has keys to manage
	}
	if row.Status != model.StatusConnected {
		return nil, errNotReady(row.Status)
	}
	return km, nil
}

// KeysStatus reports the account's encryption identity.
func (c *Core) KeysStatus(ctx context.Context, accountID string) (model.KeyStatus, error) {
	km, err := c.keyManager(ctx, accountID)
	if err != nil {
		return model.KeyStatus{}, err
	}
	return km.KeysStatus(ctx, accountID)
}

// KeysVerify establishes trust with a recovery key.
func (c *Core) KeysVerify(ctx context.Context, accountID, recoveryKey string) (model.KeyVerifyResult, error) {
	if strings.TrimSpace(recoveryKey) == "" {
		return model.KeyVerifyResult{}, errInvalid("recovery_key is required")
	}
	km, err := c.keyManager(ctx, accountID)
	if err != nil {
		return model.KeyVerifyResult{}, err
	}
	return km.KeysVerify(ctx, accountID, recoveryKey)
}

// KeysExport returns an encrypted key file.
func (c *Core) KeysExport(ctx context.Context, accountID, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, errInvalid("passphrase is required")
	}
	km, err := c.keyManager(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return km.KeysExport(ctx, accountID, passphrase)
}

// KeysImport loads an encrypted key file; it returns the number of sessions added.
func (c *Core) KeysImport(ctx context.Context, accountID, passphrase string, data []byte) (int, error) {
	if passphrase == "" || len(data) == 0 {
		return 0, errInvalid("passphrase and file are required")
	}
	km, err := c.keyManager(ctx, accountID)
	if err != nil {
		return 0, err
	}
	return km.KeysImport(ctx, accountID, passphrase, data)
}
