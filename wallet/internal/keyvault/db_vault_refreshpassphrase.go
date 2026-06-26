package keyvault

import (
	"bytes"
	"context"
	"fmt"

	"github.com/btcsuite/btcwallet/snacl"
	"github.com/btcsuite/btcwallet/wallet/internal/db"
)

// RefreshPrivatePassphrase rotates persisted wallet secrets to the new private
// passphrase and keeps the existing unlocked runtime state unchanged.
func (v *DBVault) RefreshPrivatePassphrase(ctx context.Context,
	passphrase []byte) error {

	req := vaultRefreshPrivatePassphraseReq{
		ctx:        ctx,
		passphrase: passphrase,
		resp:       make(vaultErrResp, 1),
	}

	err := v.sendReq(ctx, req)
	if err != nil {
		return err
	}

	return waitForErr(ctx, req.resp)
}

// handleRefreshPrivatePassphraseReq rotates persisted private secrets while
// preserving runtime state.
func (v *DBVault) handleRefreshPrivatePassphraseReq(state *unlockedState,
	req vaultRefreshPrivatePassphraseReq) error {

	if state == nil {
		return fmt.Errorf("wallet %d vault RefreshPrivatePassphrase: %w",
			v.walletID, ErrVaultLocked)
	}

	secrets, err := v.store.GetWalletSecrets(req.ctx, v.walletID)
	if err != nil {
		return fmt.Errorf("wallet %d vault RefreshPrivatePassphrase: "+
			"get secrets: %w", v.walletID, err)
	}

	updateParams, err := v.makeRotatedWalletSecrets(
		state, secrets, req.passphrase,
	)
	if err != nil {
		return fmt.Errorf("wallet %d vault RefreshPrivatePassphrase: "+
			"rotate secrets: %w", v.walletID, err)
	}

	err = validateRotatedWalletSecrets(state, updateParams, req.passphrase)
	if err != nil {
		return fmt.Errorf("wallet %d vault RefreshPrivatePassphrase: "+
			"validate rotated secrets: %w", v.walletID, err)
	}

	err = v.store.UpdateWalletSecrets(req.ctx, updateParams)
	if err != nil {
		return fmt.Errorf("wallet %d vault RefreshPrivatePassphrase: "+
			"update secrets: %w", v.walletID, err)
	}

	return nil
}

// validateRotatedWalletSecrets confirms rotated persisted secrets decrypt with
// the new passphrase to the same runtime keys already held in memory.
func validateRotatedWalletSecrets(currentState *unlockedState,
	params db.UpdateWalletSecretsParams, passphrase []byte) error {

	updatedSecrets := db.WalletSecrets{
		MasterPrivParams:         params.MasterPrivParams,
		EncryptedCryptoPrivKey:   params.EncryptedCryptoPrivKey,
		EncryptedCryptoScriptKey: params.EncryptedCryptoScriptKey,
		EncryptedMasterHdPrivKey: params.EncryptedMasterHdPrivKey,
	}

	validatedState, err := decryptWalletSecrets(&updatedSecrets, passphrase)
	if err != nil {
		return fmt.Errorf("decrypt rotated secrets: %w", err)
	}
	defer validatedState.zero()

	if !unlockedStateEqual(currentState, validatedState) {
		return fmt.Errorf("rotated secrets changed runtime keys: %w",
			errUnexpectedState)
	}

	return nil
}

// unlockedStateEqual reports whether two unlocked states hold equal runtime
// keys.
func unlockedStateEqual(a, b *unlockedState) bool {
	if a == nil || b == nil {
		return a == b
	}

	if !bytes.Equal(a.cryptoKeyPrivate[:], b.cryptoKeyPrivate[:]) {
		return false
	}

	if !bytes.Equal(a.cryptoKeyScript[:], b.cryptoKeyScript[:]) {
		return false
	}

	if a.hdRootKey == nil || b.hdRootKey == nil {
		return a.hdRootKey == b.hdRootKey
	}

	return a.hdRootKey.String() == b.hdRootKey.String()
}

// makeRotatedWalletSecrets creates a persisted wallet secret update encrypted
// with a new private passphrase from the currently unlocked runtime state.
func (v *DBVault) makeRotatedWalletSecrets(state *unlockedState,
	secrets *db.WalletSecrets, newPassphrase []byte) (
	db.UpdateWalletSecretsParams, error) {

	if secrets == nil {
		return db.UpdateWalletSecretsParams{},
			fmt.Errorf("missing wallet secrets: %w", errUnexpectedState)
	}

	// first, we need to load the old key parameters to be able to derive the
	// new key.
	var currentMasterPrivateKey snacl.SecretKey

	err := currentMasterPrivateKey.Unmarshal(secrets.MasterPrivParams)
	if err != nil {
		return db.UpdateWalletSecretsParams{}, fmt.Errorf(
			"unmarshal master private parameters: %w", err,
		)
	}
	defer currentMasterPrivateKey.Zero()

	keyParams := currentMasterPrivateKey.Parameters

	// second, generate the new key.
	newMasterPrivateKey, err := snacl.NewSecretKey(
		&newPassphrase, keyParams.N, keyParams.R, keyParams.P,
	)
	if err != nil {
		return db.UpdateWalletSecretsParams{},
			fmt.Errorf("new master private key: %w", err)
	}
	defer newMasterPrivateKey.Zero()

	// third, reencrypt the already unlocked material and return it.
	encryptedCryptoKeyPrivate, err := newMasterPrivateKey.Encrypt(
		state.cryptoKeyPrivate[:],
	)
	if err != nil {
		return db.UpdateWalletSecretsParams{},
			fmt.Errorf("encrypt crypto key private: %w", err)
	}

	encryptedCryptoKeyScript, err := newMasterPrivateKey.Encrypt(
		state.cryptoKeyScript[:],
	)
	if err != nil {
		return db.UpdateWalletSecretsParams{},
			fmt.Errorf("encrypt crypto key script: %w", err)
	}

	var encryptedHDRootKey []byte
	if state.hdRootKey != nil {
		encryptedHDRootKey, err = state.cryptoKeyPrivate.Encrypt(
			[]byte(state.hdRootKey.String()),
		)
		if err != nil {
			return db.UpdateWalletSecretsParams{},
				fmt.Errorf("encrypt master HD private key: %w", err)
		}
	}

	return db.UpdateWalletSecretsParams{
		WalletID:                 v.walletID,
		MasterPrivParams:         newMasterPrivateKey.Marshal(),
		EncryptedCryptoPrivKey:   encryptedCryptoKeyPrivate,
		EncryptedCryptoScriptKey: encryptedCryptoKeyScript,
		EncryptedMasterHdPrivKey: encryptedHDRootKey,
	}, nil
}
