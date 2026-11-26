package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/btcsuite/btcwallet/wallet/internal/db"
	"github.com/btcsuite/btcwallet/wallet/internal/sql/sqlite/sqlc"
)

// UpdateWallet updates various properties of a wallet, such as its
// birthday, birthday block, or sync state. The specific fields to
// update are provided in the UpdateWalletParams struct. It returns an
// error if the update fails.
func (s *Store) UpdateWallet(ctx context.Context,
	params db.UpdateWalletParams) error {

	return s.execWrite(ctx, func(qtx *sqlc.Queries) error {
		return db.UpdateWalletWithOps(
			ctx, params, updateWalletOps{q: qtx},
		)
	})
}

// updateWalletOps adapts SQLite sqlc queries to the shared UpdateWallet
// workflow.
type updateWalletOps struct {
	q *sqlc.Queries
}

// Ensure updateWalletOps implements db.UpdateWalletOps at compile time.
var _ db.UpdateWalletOps = (*updateWalletOps)(nil)

// EnsureBlock implements db.UpdateWalletOps.
func (o updateWalletOps) EnsureBlock(ctx context.Context,
	walletID uint32, block *db.Block) error {

	return ensureBlockExists(ctx, o.q, walletID, block)
}

// UpdateWalletSyncState implements db.UpdateWalletOps.
func (o updateWalletOps) UpdateWalletSyncState(ctx context.Context,
	params db.UpdateWalletParams) error {

	syncParams, err := buildUpdateSyncParams(ctx, o.q, params)
	if err != nil {
		return err
	}

	rowsAffected, err := o.q.UpdateWalletSyncState(ctx, syncParams)
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("wallet sync state for wallet %d: %w",
			params.WalletID, db.ErrWalletNotFound)
	}

	return nil
}

// buildUpdateSyncParams constructs the UpdateWalletSyncStateParams from
// the given UpdateWalletParams.
func buildUpdateSyncParams(ctx context.Context, q *sqlc.Queries,
	params db.UpdateWalletParams) (sqlc.UpdateWalletSyncStateParams, error) {

	syncParams := sqlc.UpdateWalletSyncStateParams{
		WalletID: int64(params.WalletID),
	}

	if params.SyncedTo != nil {
		id, err := requireBlockMatches(ctx, q, params.SyncedTo)
		if err != nil {
			return syncParams, err
		}

		err = linkNextSyncedBlock(ctx, q, params.WalletID, params.SyncedTo, id)
		if err != nil {
			return syncParams, err
		}

		syncParams.SyncedBlockID = sql.NullInt64{Int64: id, Valid: true}
		syncParams.SyncedHeight = sql.NullInt64{
			Int64: int64(params.SyncedTo.Height),
			Valid: true,
		}
	}

	if params.Birthday != nil {
		syncParams.BirthdayTimestamp = sql.NullTime{
			Time:  *params.Birthday,
			Valid: true,
		}
	}

	if params.BirthdayBlock != nil {
		id, err := requireBlockMatches(ctx, q, params.BirthdayBlock)
		if err != nil {
			return syncParams, err
		}

		syncParams.BirthdayBlockID = sql.NullInt64{Int64: id, Valid: true}
		syncParams.BirthdayHeight = sql.NullInt64{
			Int64: int64(params.BirthdayBlock.Height),
			Valid: true,
		}
	}

	return syncParams, nil
}
