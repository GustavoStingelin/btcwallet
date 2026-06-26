package keyvault

import (
	"context"
	"fmt"
	"time"

	"github.com/btcsuite/btcwallet/waddrmgr"
)

// vaultErrResp carries the result of a vault request that only returns an
// error.
type vaultErrResp chan error

// vaultBoolResp carries the result of a vault request that returns a boolean.
type vaultBoolResp chan bool

// vaultBytesResp carries the result of a vault request that returns bytes and
// an error.
type vaultBytesResp chan vaultBytesResult

// vaultBytesResult is the response for cryptographic byte operations.
type vaultBytesResult struct {
	value []byte
	err   error
}

// vaultUnlockReq requests that the vault load and decrypt runtime secrets.
type vaultUnlockReq struct {
	ctx        context.Context
	passphrase []byte
	timeout    time.Duration
	resp       vaultErrResp
}

// vaultLockReq requests that the vault clear runtime secrets.
type vaultLockReq struct {
	resp vaultErrResp
}

// vaultIsLockedReq requests the current lock state.
type vaultIsLockedReq struct {
	resp vaultBoolResp
}

// vaultEncryptReq requests encryption with an unlocked runtime key.
type vaultEncryptReq struct {
	keyType   waddrmgr.CryptoKeyType
	plaintext []byte
	resp      vaultBytesResp
}

// vaultDecryptReq requests decryption with an unlocked runtime key.
type vaultDecryptReq struct {
	keyType    waddrmgr.CryptoKeyType
	ciphertext []byte
	resp       vaultBytesResp
}

// vaultRefreshPrivatePassphraseReq requests private passphrase rotation.
type vaultRefreshPrivatePassphraseReq struct {
	ctx        context.Context
	passphrase []byte
	resp       vaultErrResp
}

// mainLoop serializes vault state changes, cryptographic requests, and
// auto-lock timer events.
func (v *DBVault) mainLoop() {
	var state *unlockedState
	var lockTimer *time.Timer
	var lockTimerC <-chan time.Time

	for {
		select {
		case req := <-v.requests:
			switch r := req.(type) {
			case vaultUnlockReq:
				state, lockTimer, lockTimerC = v.handleUnlockReq(
					state, lockTimer, r,
				)

			case vaultLockReq:
				state, lockTimer, lockTimerC = v.handleLockReq(
					state, lockTimer, r,
				)

			case vaultIsLockedReq:
				v.handleIsLockedReq(state, r)

			case vaultEncryptReq:
				r.resp <- v.handleEncryptReq(state, r)

			case vaultDecryptReq:
				r.resp <- v.handleDecryptReq(state, r)

			case vaultRefreshPrivatePassphraseReq:
				r.resp <- v.handleRefreshPrivatePassphraseReq(state, r)

			default:
				panic(fmt.Sprintf("DBVault received unknown request type: %T", req))
			}

		case <-lockTimerC:
			state = clearRuntimeState(state)
			lockTimer = nil
			lockTimerC = nil
		}
	}
}

// sendReq sends a request to the vault actor or returns context cancellation.
func (v *DBVault) sendReq(ctx context.Context, req any) error {
	select {
	case v.requests <- req:
		return nil

	case <-ctx.Done():
		return ctx.Err()
	}
}

// waitForErr waits for an error response or context cancellation.
func waitForErr(ctx context.Context, resp <-chan error) error {
	select {
	case err := <-resp:
		return err

	case <-ctx.Done():
		return ctx.Err()
	}
}

// waitForBytes waits for a byte response or context cancellation.
func waitForBytes(ctx context.Context,
	resp <-chan vaultBytesResult) ([]byte, error) {

	select {
	case result := <-resp:
		return result.value, result.err

	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// clearRuntimeState erases the unlocked state and returns the locked state.
func clearRuntimeState(state *unlockedState) *unlockedState {
	if state != nil {
		state.zero()
	}

	return nil
}

// scheduleAutoLockTimer creates the next active auto-lock timer.
func scheduleAutoLockTimer(timeout time.Duration) *time.Timer {
	if timeout < 0 {
		return nil
	}

	if timeout == 0 {
		timeout = defaultVaultUnlockTimeout
	}

	return time.NewTimer(timeout)
}

// stopAutoLockTimer stops and drains an active auto-lock timer.
func stopAutoLockTimer(timer *time.Timer) *time.Timer {
	if timer == nil {
		return nil
	}

	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}

	return nil
}

// timerChan returns the active timer channel, if a timer is scheduled.
func timerChan(timer *time.Timer) <-chan time.Time {
	if timer == nil {
		return nil
	}

	return timer.C
}
