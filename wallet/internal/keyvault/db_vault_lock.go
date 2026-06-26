package keyvault

import (
	"time"
)

// Lock locks the vault, clears any pending automatic lock, and erases runtime
// secret material from memory.
func (v *DBVault) Lock() {
	req := vaultLockReq{
		resp: make(vaultErrResp, 1),
	}

	v.requests <- req
	<-req.resp
}

// handleLockReq clears runtime state, stops the auto-lock timer, and reports
// the vault as locked.
func (v *DBVault) handleLockReq(state *unlockedState, lockTimer *time.Timer,
	req vaultLockReq) (*unlockedState, *time.Timer, <-chan time.Time) {

	state = clearRuntimeState(state)
	lockTimer = stopAutoLockTimer(lockTimer)
	req.resp <- nil

	return state, lockTimer, nil
}
