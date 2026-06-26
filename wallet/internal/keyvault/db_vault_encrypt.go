package keyvault

import (
	"context"
	"errors"
	"fmt"

	"github.com/btcsuite/btcwallet/snacl"
	"github.com/btcsuite/btcwallet/waddrmgr"
)

// errUnsupportedCryptoKeyType is returned when a crypto key type is not
// supported for the requested operation.
var errUnsupportedCryptoKeyType = errors.New("unsupported crypto key type")

// Encrypt encrypts plaintext with the selected unlocked runtime crypto key.
func (v *DBVault) Encrypt(keyType waddrmgr.CryptoKeyType,
	plaintext []byte) ([]byte, error) {

	req := vaultEncryptReq{
		keyType:   keyType,
		plaintext: plaintext,
		resp:      make(vaultBytesResp, 1),
	}

	v.requests <- req

	return waitForBytes(context.Background(), req.resp)
}

// handleEncryptReq encrypts plaintext with the selected runtime key.
func (v *DBVault) handleEncryptReq(state *unlockedState,
	req vaultEncryptReq) vaultBytesResult {

	if state == nil {
		return vaultBytesResult{
			err: fmt.Errorf("wallet %d vault Encrypt: %w", v.walletID,
				ErrVaultLocked),
		}
	}

	cryptoKey, err := selectUnlockedCryptoKey(state, req.keyType)
	if err != nil {
		return vaultBytesResult{
			err: fmt.Errorf("wallet %d vault Encrypt: %w", v.walletID, err),
		}
	}

	ciphertext, err := cryptoKey.Encrypt(req.plaintext)
	if err != nil {
		return vaultBytesResult{
			err: fmt.Errorf("wallet %d vault Encrypt: encrypt: %w",
				v.walletID, err),
		}
	}

	return vaultBytesResult{value: ciphertext}
}

// selectUnlockedCryptoKey returns a crypto key available in unlockedState.
func selectUnlockedCryptoKey(state *unlockedState,
	keyType waddrmgr.CryptoKeyType) (*snacl.CryptoKey, error) {

	switch keyType {
	case waddrmgr.CKTPrivate:
		return &state.cryptoKeyPrivate, nil
	case waddrmgr.CKTScript:
		return &state.cryptoKeyScript, nil
	case waddrmgr.CKTPublic:
		return nil, fmt.Errorf("public crypto key: %w",
			errUnsupportedCryptoKeyType)
	default:
		return nil, fmt.Errorf("%d: %w", keyType, errUnsupportedCryptoKeyType)
	}
}
