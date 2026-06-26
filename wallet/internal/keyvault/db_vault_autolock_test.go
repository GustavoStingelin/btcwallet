package keyvault

import (
	"testing"
	"time"

	bwmock "github.com/btcsuite/btcwallet/wallet/internal/bwtest/mock"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestDBVaultAutoLockTimeout verifies that a positive timeout automatically
// locks the vault.
func TestDBVaultAutoLockTimeout(t *testing.T) {
	t.Parallel()

	vault, _ := unlockTestVault(t, 10, 10*time.Millisecond)
	require.False(t, vault.IsLocked())

	require.Eventually(
		t, vault.IsLocked, time.Second, time.Millisecond,
		"vault did not lock before timeout",
	)
}

// TestDBVaultAutoLockNegativeTimeoutDisabled verifies that a negative timeout
// leaves the vault unlocked until an explicit Lock.
func TestDBVaultAutoLockNegativeTimeoutDisabled(t *testing.T) {
	t.Parallel()

	vault, _ := unlockTestVault(t, 11, -1)
	require.False(t, vault.IsLocked())

	require.Never(
		t, vault.IsLocked, 30*time.Millisecond, time.Millisecond,
		"negative timeout should disable automatic locking",
	)

	vault.Lock()
	require.True(t, vault.IsLocked())
}

// TestDBVaultAutoLockZeroTimeoutUsesDefault verifies that a zero timeout uses
// the package default rather than firing immediately.
func TestDBVaultAutoLockZeroTimeoutUsesDefault(t *testing.T) {
	t.Parallel()

	timer := scheduleAutoLockTimer(0)
	require.NotNil(t, timer)
	t.Cleanup(func() {
		stopAutoLockTimer(timer)
	})

	require.Never(
		t, func() bool {
			select {
			case <-timer.C:
				return true
			default:
				return false
			}
		}, 20*time.Millisecond, time.Millisecond,
		"default timeout should not fire immediately",
	)
}

// TestDBVaultAutoLockStaleTimerDoesNotExpireReplacement verifies that replacing
// an unlock timeout prevents the older timeout from clearing newer state.
func TestDBVaultAutoLockStaleTimerDoesNotExpireReplacement(t *testing.T) {
	t.Parallel()

	secrets, _ := makeWalletSecrets(t, correctPassphrase)

	const walletID = uint32(17)

	store := new(bwmock.Store)
	store.On("GetWalletSecrets", mock.Anything, walletID).Return(
		secrets, nil,
	).Once()
	store.On("GetWalletSecrets", mock.Anything, walletID).Return(
		secrets, nil,
	).Once()
	t.Cleanup(func() {
		store.AssertExpectations(t)
	})

	vault := NewDBVault(store, walletID)
	require.NoError(t, vault.Unlock(t.Context(), correctPassphrase,
		20*time.Millisecond))
	require.NoError(t, vault.Unlock(t.Context(), correctPassphrase,
		200*time.Millisecond))
	t.Cleanup(vault.Lock)

	require.Never(
		t, vault.IsLocked, 70*time.Millisecond, time.Millisecond,
		"stale timer from first unlock locked replacement state",
	)
}

// TestDBVaultExplicitLockInvalidatesPendingTimer verifies that Lock clears a
// pending timeout so it cannot lock a later unlock.
func TestDBVaultExplicitLockInvalidatesPendingTimer(t *testing.T) {
	t.Parallel()

	secrets, _ := makeWalletSecrets(t, correctPassphrase)

	const walletID = uint32(18)

	store := new(bwmock.Store)
	store.On("GetWalletSecrets", mock.Anything, walletID).Return(
		secrets, nil,
	).Once()
	store.On("GetWalletSecrets", mock.Anything, walletID).Return(
		secrets, nil,
	).Once()
	t.Cleanup(func() {
		store.AssertExpectations(t)
	})

	vault := NewDBVault(store, walletID)
	require.NoError(t, vault.Unlock(t.Context(), correctPassphrase,
		20*time.Millisecond))
	vault.Lock()
	require.True(t, vault.IsLocked())

	require.NoError(t, vault.Unlock(t.Context(), correctPassphrase, -1))
	t.Cleanup(vault.Lock)

	require.Never(
		t, vault.IsLocked, 70*time.Millisecond, time.Millisecond,
		"pending timer from explicitly locked state locked replacement state",
	)
}
