//go:build itest

package itest

import (
	"testing"

	"github.com/btcsuite/btcwallet/wallet/internal/db"
	"github.com/stretchr/testify/require"
)

// TestListSyncedBlocksRejectsMissingRanges verifies missing block heights are
// reported for both an interior gap and a missing range tail.
func TestListSyncedBlocksRejectsMissingRanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query db.ListSyncedBlocksQuery
	}{
		{
			name: "gap",
			query: db.ListSyncedBlocksQuery{
				StartHeight: 144,
				EndHeight:   146,
			},
		},
		{
			name: "short tail",
			query: db.ListSyncedBlocksQuery{
				StartHeight: 146,
				EndHeight:   147,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange: persist blocks on either side of the missing range.
			store := NewTestStore(t)
			walletID := newWallet(t, store, "range-owner")
			left, right := NewBlockFixture(144), NewBlockFixture(146)
			require.NoError(t, store.UpdateWallet(
				t.Context(), db.UpdateWalletParams{
					WalletID: walletID, BirthdayBlock: &left,
					SyncedTo: &right,
				},
			))

			// Act: request a range containing a missing height.
			query := test.query
			query.WalletID = walletID
			_, err := store.ListSyncedBlocks(t.Context(), query)

			// Assert: missing ranges return the shared not-found error.
			require.ErrorIs(t, err, db.ErrBlockNotFound)
		})
	}
}

// TestInsertTreeBlockConstraints admits competing hashes, ignores idempotent
// replays and preserves SQL constraint failures.
func TestInsertTreeBlockConstraints(t *testing.T) {
	store := NewTestStore(t)
	queries := store.Queries()
	hash, competing, invalid := [32]byte{1}, [32]byte{2}, [32]byte{3}
	block := insertTreeBlockParams{
		BlockHeight:    1,
		BlockHash:      hash[:],
		BlockTimestamp: 1,
	}
	ctx := t.Context()
	require.NoError(t, queries.InsertTreeBlock(ctx, block))
	require.NoError(t, queries.InsertTreeBlock(ctx, block))
	require.NoError(t, queries.InsertTreeBlock(ctx, insertTreeBlockParams{
		BlockHeight: 1, BlockHash: competing[:], BlockTimestamp: 2,
	}))

	err := queries.InsertTreeBlock(ctx, insertTreeBlockParams{
		BlockHeight: 3, BlockHash: invalid[:], BlockTimestamp: -1,
	})
	requireDriverConstraintError(t, err, driverConstraintCheck)

	found, err := queries.TreeBlockByHash(ctx, hash[:])
	require.NoError(t, err)
	fork, err := queries.TreeBlockByHash(ctx, competing[:])
	require.NoError(t, err)
	require.Equal(t, found.BlockHeight, fork.BlockHeight)
	require.NotEqual(t, found.ID, fork.ID)
	require.Equal(t, block.BlockTimestamp, found.BlockTimestamp)
}
