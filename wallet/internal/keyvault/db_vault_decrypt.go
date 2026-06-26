package keyvault

import (
	"context"
	"fmt"

	"github.com/btcsuite/btcwallet/waddrmgr"
)

// Decrypt decrypts ciphertext with the selected unlocked runtime crypto key.
func (v *DBVault) Decrypt(keyType waddrmgr.CryptoKeyType,
	ciphertext []byte) ([]byte, error) {

	req := vaultDecryptReq{
		keyType:    keyType,
		ciphertext: ciphertext,
		resp:       make(vaultBytesResp, 1),
	}

	v.requests <- req

	return waitForBytes(context.Background(), req.resp)
}

// handleDecryptReq decrypts ciphertext with the selected runtime key.
func (v *DBVault) handleDecryptReq(state *unlockedState,
	req vaultDecryptReq) vaultBytesResult {

	if state == nil {
		return vaultBytesResult{
			err: fmt.Errorf("wallet %d vault Decrypt: %w", v.walletID,
				ErrVaultLocked),
		}
	}

	cryptoKey, err := selectUnlockedCryptoKey(state, req.keyType)
	if err != nil {
		return vaultBytesResult{
			err: fmt.Errorf("wallet %d vault Decrypt: %w", v.walletID, err),
		}
	}

	plaintext, err := cryptoKey.Decrypt(req.ciphertext)
	if err != nil {
		return vaultBytesResult{
			err: fmt.Errorf("wallet %d vault Decrypt: decrypt: %w",
				v.walletID, err),
		}
	}

	return vaultBytesResult{value: plaintext}
}
