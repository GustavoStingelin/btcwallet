package keyvault

import (
	"testing"
	"time"

	bwmock "github.com/btcsuite/btcwallet/wallet/internal/bwtest/mock"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestDBVaultLockClearsUnlockedState verifies that Lock returns the vault to
// the locked state and wipes runtime secrets.
func TestDBVaultLockClearsUnlockedState(t *testing.T) {
	t.Parallel()

	vault, _ := unlockTestVault(t, 1, -1)

	require.False(t, vault.IsLocked())

	vault.Lock()

	require.True(t, vault.IsLocked())
}

// TestDBVaultLockIdempotent verifies that Lock stays a no-op when already
// locked.
func TestDBVaultLockIdempotent(t *testing.T) {
	t.Parallel()

	vault := NewDBVault(nil, 1)
	require.True(t, vault.IsLocked())

	vault.Lock()

	require.True(t, vault.IsLocked())

	vault.Lock()
	require.True(t, vault.IsLocked())
}

// TestDBVaultLockWaitsForInFlightUnlock verifies that explicit Lock calls are
// ordered after an Unlock that has already entered the vault lifecycle.
func TestDBVaultLockWaitsForInFlightUnlock(t *testing.T) {
	t.Parallel()

	secrets, _ := makeWalletSecrets(t, correctPassphrase)

	const walletID = uint32(12)

	unlockStarted := make(chan struct{})
	releaseUnlock := make(chan struct{})

	store := new(bwmock.Store)
	store.On("GetWalletSecrets", mock.Anything, walletID).Run(
		func(_ mock.Arguments) {
			// close unlockStarted to signal that Unlock has entered the store
			// call and is now in-flight.
			close(unlockStarted)

			// wait for releaseUnlock to be closed before returning, simulating
			// a long-running store call that holds the vault lifecycle lock
			// until completion.
			<-releaseUnlock
		},
	).Return(secrets, nil).Once()
	t.Cleanup(func() {
		store.AssertExpectations(t)
	})

	vault := NewDBVault(store, walletID)

	unlockDone := make(chan error, 1)

	// run Unlock in a goroutine so Lock can contend with an in-flight unlock.
	go func() {
		unlockDone <- vault.Unlock(t.Context(), correctPassphrase, -1)
	}()

	require.Eventually(
		t, func() bool {
			select {
			case <-unlockStarted:
				return true
			default:
				return false
			}
		}, time.Second, time.Millisecond, "Unlock did not start",
	)

	// start Lock while Unlock is blocked so we can verify Lock waits for the
	// in-flight Unlock to finish.
	lockDone := make(chan struct{})
	go func() {
		vault.Lock()
		close(lockDone)
	}()

	require.Never(
		t, func() bool {
			select {
			case <-lockDone:
				return true
			default:
				return false
			}
		}, time.Second, time.Millisecond,
		"Lock completed before the in flight Unlock finished",
	)

	// releasing the in-flight Unlock should allow both the Unlock and Lock to
	// complete in order.
	close(releaseUnlock)

	require.Eventually(t, func() bool {
		select {
		case err := <-unlockDone:
			require.NoError(t, err)
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond,
		"Unlock did not complete after being released")

	require.Eventually(t, func() bool {
		select {
		case <-lockDone:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond,
		"Lock did not complete after the in flight Unlock finished")

	require.True(t, vault.IsLocked())
}
