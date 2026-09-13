package matrix

import (
	"context"
	"net/http"
	"strings"

	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/crypto/backup"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

func (a *Adapter) machine(accountID string) (*account, *crypto.OlmMachine, error) {
	acc, _, err := a.online(accountID)
	if err != nil {
		return nil, nil, err
	}
	acc.mu.Lock()
	helper := acc.crypto
	acc.mu.Unlock()
	if helper == nil {
		return nil, nil, adapter.Errorf(adapter.ErrNotConnected, "encryption is not initialised for this account")
	}
	return acc, helper.Machine(), nil
}

func countSessions(ctx context.Context, mach *crypto.OlmMachine) int {
	iter := mach.CryptoStore.GetAllGroupSessions(ctx)
	if iter == nil {
		return 0
	}
	n := 0
	_ = iter.Iter(func(*crypto.InboundGroupSession) (bool, error) { n++; return true, nil })
	return n
}

// KeysStatus reports the device identity, cross-signing trust, backup, and session count.
func (a *Adapter) KeysStatus(ctx context.Context, accountID string) (model.KeyStatus, error) {
	_, mach, err := a.machine(accountID)
	if err != nil {
		return model.KeyStatus{}, err
	}
	own := mach.OwnIdentity()
	trust, _ := mach.ResolveTrustContext(ctx, own)
	st := model.KeyStatus{
		DeviceID: own.DeviceID.String(), Fingerprint: own.Fingerprint(),
		CrossSigned: trust >= id.TrustStateCrossSignedTOFU,
		Sessions:    countSessions(ctx, mach),
	}
	st.Backup.Version = string(mach.KeyBackupVersion())
	st.Backup.Enabled = st.Backup.Version != ""
	return st, nil
}

// KeysVerify uses the recovery key to cross-sign this device and restore the server-side key
// backup, so other clients trust the bridge and history received before login decrypts.
func (a *Adapter) KeysVerify(ctx context.Context, accountID, recoveryKey string) (model.KeyVerifyResult, error) {
	_, mach, err := a.machine(accountID)
	if err != nil {
		return model.KeyVerifyResult{}, err
	}
	keyID, keyData, err := mach.SSSS.GetDefaultKeyData(ctx)
	if err != nil {
		return model.KeyVerifyResult{}, base.PlatformErr("secret storage", err)
	}
	key, err := keyData.VerifyRecoveryKey(keyID, strings.TrimSpace(recoveryKey))
	if err != nil {
		return model.KeyVerifyResult{}, adapter.Errorf(adapter.ErrInvalidInput, "recovery key rejected: %v", err)
	}
	res := model.KeyVerifyResult{}
	if err := mach.FetchCrossSigningKeysFromSSSS(ctx, key); err != nil {
		return res, base.PlatformErr("cross-signing keys", err)
	}
	if err := mach.SignOwnDevice(ctx, mach.OwnIdentity()); err != nil {
		return res, base.PlatformErr("sign device", err)
	}
	res.CrossSigned = true

	before := countSessions(ctx, mach)
	data, err := mach.SSSS.GetDecryptedAccountData(ctx, event.AccountDataMegolmBackupKey, key)
	if err != nil {
		return res, nil // cross-signed, but no key backup configured on this account
	}
	mbk, err := backup.MegolmBackupKeyFromBytes(data)
	if err != nil {
		return res, base.PlatformErr("backup key", err)
	}
	latest, err := mach.GetAndVerifyLatestKeyBackupVersion(ctx, mbk)
	if err != nil {
		if httpStatus(err) == http.StatusNotFound {
			return res, nil // no server-side backup on this account
		}
		return res, base.PlatformErr("key backup", err)
	}
	res.BackupVersion = string(latest.Version)
	if _, err := mach.DownloadAndStoreLatestKeyBackup(ctx, mbk); err != nil {
		return res, base.PlatformErr("restore key backup", err)
	}
	res.SessionsImported = countSessions(ctx, mach) - before
	return res, nil
}

// KeysExport writes every known megolm session as an Element-compatible encrypted file.
func (a *Adapter) KeysExport(ctx context.Context, accountID, passphrase string) ([]byte, error) {
	_, mach, err := a.machine(accountID)
	if err != nil {
		return nil, err
	}
	data, err := crypto.ExportKeysIter(passphrase, mach.CryptoStore.GetAllGroupSessions(ctx))
	if err != nil {
		return nil, base.PlatformErr("export keys", err)
	}
	return data, nil
}

// KeysImport loads sessions from an exported file; it returns how many were new.
func (a *Adapter) KeysImport(ctx context.Context, accountID, passphrase string, data []byte) (int, error) {
	_, mach, err := a.machine(accountID)
	if err != nil {
		return 0, err
	}
	imported, _, err := mach.ImportKeys(ctx, passphrase, data)
	if err != nil {
		return 0, adapter.Errorf(adapter.ErrInvalidInput, "import keys: %v", err)
	}
	return imported, nil
}
