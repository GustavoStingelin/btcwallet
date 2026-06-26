package keyvault

import (
	"testing"

	"github.com/btcsuite/btcwallet/snacl"
	"github.com/btcsuite/btcwallet/waddrmgr"
	"github.com/stretchr/testify/require"
)

// TestDBVaultEncryptSelectedRuntimeKeys verifies that Encrypt uses the selected
// runtime crypto key and preserves snacl ciphertext semantics.
func TestDBVaultEncryptSelectedRuntimeKeys(t *testing.T) {
	t.Parallel()

	vault, expected := unlockTestVault(t, 20, -1)

	tests := []struct {
		name    string
		keyType waddrmgr.CryptoKeyType
		key     *snacl.CryptoKey
	}{
		{
			name:    "private key",
			keyType: waddrmgr.CKTPrivate,
			key:     &expected.cryptoKeyPrivate,
		},
		{
			name:    "script key",
			keyType: waddrmgr.CKTScript,
			key:     &expected.cryptoKeyScript,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			plaintext := []byte("key material for " + test.name)
			ciphertext, err := vault.Encrypt(test.keyType, plaintext)
			require.NoError(t, err)
			require.NotEqual(t, plaintext, ciphertext)
			require.Len(t, ciphertext, len(plaintext)+snacl.NonceSize+
				snacl.Overhead)

			decrypted, err := test.key.Decrypt(ciphertext)
			require.NoError(t, err)
			require.Equal(t, plaintext, decrypted)
		})
	}
}

// TestDBVaultEncryptLocked verifies that missing runtime state reports the
// vault locked sentinel with wallet and method context.
func TestDBVaultEncryptLocked(t *testing.T) {
	t.Parallel()

	vault := NewDBVault(nil, 1)
	ciphertext, err := vault.Encrypt(waddrmgr.CKTPrivate, []byte("plaintext"))
	require.Nil(t, ciphertext)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrVaultLocked)
	require.ErrorContains(t, err, "wallet 1 vault Encrypt")
}

// TestDBVaultEncryptUnsupportedKeyTypes verifies that DBVault only exposes the
// runtime crypto keys it holds in unlockedState.
func TestDBVaultEncryptUnsupportedKeyTypes(t *testing.T) {
	t.Parallel()

	const walletID = uint32(21)
	vault, _ := unlockTestVault(t, walletID, -1)

	tests := []struct {
		name    string
		keyType waddrmgr.CryptoKeyType
		message string
	}{
		{
			name:    "public key",
			keyType: waddrmgr.CKTPublic,
			message: "public crypto key",
		},
		{
			name:    "invalid key type",
			keyType: waddrmgr.CryptoKeyType(0xff),
			message: "255",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ciphertext, err := vault.Encrypt(test.keyType, []byte("plaintext"))
			require.Nil(t, ciphertext)
			require.Error(t, err)
			require.ErrorIs(t, err, errUnsupportedCryptoKeyType)
			require.ErrorContains(t, err, "wallet 21 vault Encrypt")
			require.ErrorContains(t, err, test.message)
		})
	}
}
