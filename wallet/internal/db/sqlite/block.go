package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcwallet/wallet/internal/db"
	"github.com/btcsuite/btcwallet/wallet/internal/sql/sqlite/sqlc"
)

// buildBlock constructs a Block from the given SQLite block
// fields.
func buildBlock(height sql.NullInt64, hash []byte,
	timestamp sql.NullInt64) (*db.Block, error) {

	if !height.Valid {
		return nil, fmt.Errorf("block height: %w", db.ErrInvalidNullInt)
	}

	height32, err := db.Int64ToUint32(height.Int64)
	if err != nil {
		return nil, fmt.Errorf("block height: %w", err)
	}

	return db.BuildBlock(hash, height32, timestamp.Int64)
}

// ensureBlockExists ensures that a block exists in the database. If it
// doesn't exist, it inserts it.
func ensureBlockExists(ctx context.Context, qtx *sqlc.Queries,
	_ uint32, block *db.Block) error {

	height := int64(block.Height)

	blockParams := sqlc.InsertTreeBlockParams{
		BlockHeight:    height,
		BlockHash:      block.Hash[:],
		BlockTimestamp: block.Timestamp.Unix(),
	}

	err := qtx.InsertTreeBlock(ctx, blockParams)
	if err != nil {
		return fmt.Errorf("insert block: %w", err)
	}

	_, err = requireBlockMatches(ctx, qtx, block)
	if err != nil {
		return err
	}

	return nil
}

// requireBlockMatches resolves a shared hash to a validated block ID.
func requireBlockMatches(ctx context.Context, qtx *sqlc.Queries,
	block *db.Block) (int64, error) {

	height := int64(block.Height)

	storedBlock, err := qtx.TreeBlockByHash(ctx, block.Hash[:])
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("block %d: %w", block.Height,
				db.ErrBlockNotFound)
		}

		return 0, fmt.Errorf("get shared block by hash: %w", err)
	}

	if storedBlock.BlockHeight != height ||
		!bytes.Equal(storedBlock.BlockHash, block.Hash[:]) {

		return 0, fmt.Errorf("block %d identity: %w", height,
			db.ErrBlockMismatch)
	}

	if storedBlock.BlockTimestamp != block.Timestamp.Unix() {
		return 0, fmt.Errorf("block %d timestamp: %w", block.Height,
			db.ErrBlockMismatch)
	}

	return storedBlock.ID, nil
}

// linkNextSyncedBlock closes the edge for a verified adjacent scan advance.
// An initial checkpoint or explicit jump starts an independent anchor.
func linkNextSyncedBlock(ctx context.Context, q *sqlc.Queries,
	walletID uint32, block *db.Block, id int64) error {

	parent, err := q.TreeCheckpoint(ctx, int64(walletID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("load scanned parent: %w", err)
	}

	if parent.ID == id {
		return nil
	}

	if parent.BlockHeight+1 != int64(block.Height) {
		if block.ParentHash != (chainhash.Hash{}) {
			return fmt.Errorf("block %d skips checkpoint %d: %w",
				block.Height, parent.BlockHeight, db.ErrBlockMismatch)
		}

		return nil
	}

	if block.ParentHash != (chainhash.Hash{}) &&
		!bytes.Equal(block.ParentHash[:], parent.BlockHash) {

		return fmt.Errorf("block %d does not extend checkpoint: %w",
			block.Height, db.ErrBlockMismatch)
	}

	return linkTreeParent(ctx, q, id, parent.ID)
}

// linkTreeParent verifies that the conditional write left the expected edge.
func linkTreeParent(ctx context.Context, q *sqlc.Queries, childID,
	parentID int64) error {

	err := q.SetTreeBlockParent(ctx, sqlc.SetTreeBlockParentParams{
		ID: childID, ParentID: sql.NullInt64{Int64: parentID, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("link scanned child: %w", err)
	}

	child, err := q.TreeBlockByID(ctx, childID)
	if err != nil {
		return fmt.Errorf("read scanned child: %w", err)
	}

	if !child.ParentID.Valid || child.ParentID.Int64 != parentID {
		return fmt.Errorf("block %d parent: %w", child.BlockHeight,
			db.ErrBlockMismatch)
	}

	return nil
}
