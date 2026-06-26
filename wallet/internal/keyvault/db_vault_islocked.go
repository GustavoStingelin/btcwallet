package keyvault

// handleIsLockedReq reports whether the vault currently has unlocked runtime
// state.
func (v *DBVault) handleIsLockedReq(state *unlockedState, req vaultIsLockedReq) {
	req.resp <- state == nil
}

// IsLocked reports whether the vault currently has unlocked runtime state.
func (v *DBVault) IsLocked() bool {
	req := vaultIsLockedReq{
		resp: make(vaultBoolResp, 1),
	}

	v.requests <- req

	return <-req.resp
}
