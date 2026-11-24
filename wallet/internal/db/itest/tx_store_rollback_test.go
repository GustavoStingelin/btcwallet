//go:build itest

package itest

import (
	"testing"
	"time"

	"github.com/btcsuite/btcd/address/v2"
	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/btcsuite/btcwallet/wallet/internal/db"
	"github.com/stretchr/testify/require"
)

// TestDisconnectBlockReconcilesMissingObservation verifies that an empty
// disconnect can invalidate an older payment learned ahead of scan progress.
func TestDisconnectBlockReconcilesMissingObservation(t *testing.T) {
	t.Parallel()

	// Arrange a scan through the fork and two later confirmed transactions.
	// No payment or scan has created a row for the intervening notification.
	ctx := t.Context()
	store := NewTestStore(t)
	id := newWallet(t, store, "sparse-disconnect")
	fork, old := NewBlockFixture(1), NewBlockFixture(2)
	missing, retained := NewBlockFixture(3), NewBlockFixture(4)
	err := store.ApplyScanBatch(ctx, db.ScanBatchParams{
		WalletID: id, SyncedBlocks: []db.Block{fork},
	})
	require.NoError(t, err)

	payment := newRegularTx(
		[]wire.OutPoint{{Hash: chainhash.Hash{19}}},
		[]*wire.TxOut{wire.NewTxOut(100, nil)},
	)
	reward := newCoinbaseTx(nil)
	err = store.ApplyTxBatch(ctx, db.TxBatchParams{
		WalletID: id,
		Transactions: []db.CreateTxParams{
			{
				WalletID: id, Tx: payment, Block: &old,
				Received: old.Timestamp, Status: db.TxStatusPublished,
			},
			{
				WalletID: id, Tx: reward, Block: &retained,
				Received: retained.Timestamp, Status: db.TxStatusPublished,
			},
		},
	})
	require.NoError(t, err)

	// Act on just the missing notification. Its verified branch replaces
	// the lower payment's block and still contains the higher reward.
	replacement := old
	replacement.Hash[0] ^= 0xff
	err = store.DisconnectBlock(ctx, id, missing, &fork,
		map[uint32]*chainhash.Hash{
			old.Height:      &replacement.Hash,
			missing.Height:  {23},
			retained.Height: &retained.Hash,
		})
	require.NoError(t, err)

	// Assert the abandoned confirmation disappeared, the reward stayed
	// confirmed, and the scanner can commit the replacement identity.
	tx, err := store.GetTx(ctx, db.GetTxQuery{
		WalletID: id, Txid: payment.TxHash(),
	})
	require.NoError(t, err)
	require.Nil(t, tx.Block)
	tx, err = store.GetTx(ctx, db.GetTxQuery{
		WalletID: id, Txid: reward.TxHash(),
	})
	require.NoError(t, err)
	require.Equal(t, db.TxStatusPublished, tx.Status)
	require.NotNil(t, tx.Block)
	require.Equal(t, retained.Hash, tx.Block.Hash)
	err = store.ApplyScanBatch(ctx, db.ScanBatchParams{
		WalletID: id, SyncedBlocks: []db.Block{replacement},
	})
	require.NoError(t, err)
}

// TestRollbackPreservesStateOnFailure checks that both rollback entry points
// restore Wallet progress and transaction state if deleting the block fails.
func TestRollbackPreservesStateOnFailure(t *testing.T) {
	t.Parallel()

	// Each call needs a separate failing Store and its pre-write snapshots.
	type fixture struct {
		store  db.Store
		fork   db.Block
		wallet *db.WalletInfo
		reward *db.TxInfo
	}

	setup := func(t *testing.T) fixture {
		t.Helper()

		const name = "wallet-rollback-atomic"

		ctx := t.Context()
		store := NewTestStore(t)
		id := newWallet(t, store, name)
		createDerivedAccount(t, store, id, db.KeyScopeBIP0084, "default")
		addr := newDerivedAddress(
			t, store, id, db.KeyScopeBIP0084, "default", false,
		)

		// The trigger fires while removing the confirmed credit's block,
		// after the transaction has attempted to rewind its references.
		fork := CreateBlockFixture(t, store.Queries(), id, 419)
		tip := CreateBlockFixture(t, store.Queries(), id, 420)
		reward := newCoinbaseTx(addr.ScriptPubKey)
		err := store.CreateTx(ctx, db.CreateTxParams{
			WalletID: id,
			Tx:       reward,
			Received: time.Unix(1710004200, 0),
			Block:    &tip,
			Status:   db.TxStatusPublished,
			Credits:  map[uint32]address.Address{0: nil},
		})
		require.NoError(t, err)
		err = store.UpdateWallet(ctx, db.UpdateWalletParams{
			WalletID: id, SyncedTo: &tip,
		})
		require.NoError(t, err)

		before, err := store.GetWallet(ctx, name)
		require.NoError(t, err)
		tx, err := store.GetTx(ctx, db.GetTxQuery{
			WalletID: id, Txid: reward.TxHash(),
		})
		require.NoError(t, err)
		forceRollbackBlockDeleteFailure(t, store, id)

		return fixture{
			store:  store,
			fork:   fork,
			wallet: before,
			reward: tx,
		}
	}

	t.Run("RollbackToBlock", func(t *testing.T) {
		t.Parallel()

		// Arrange: Save the confirmed credit and checkpoint in a Store
		// that will reject the final deletion of that checkpoint.
		ctx := t.Context()
		f := setup(t)

		// Act: Request a height-based rollback of the confirmed block.
		err := f.store.RollbackToBlock(
			ctx, f.wallet.ID, f.wallet.SyncedTo.Height,
		)

		// Assert: Verify the injected error and read back both complete
		// snapshots to catch a partially committed rollback.
		requireDriverConstraintError(t, err, driverRollbackDeleteFailure)
		require.ErrorContains(t, err, "forced rollback block delete failure")
		after, err := f.store.GetWallet(ctx, f.wallet.Name)
		require.NoError(t, err)
		require.Equal(t, f.wallet, after)
		tx, err := f.store.GetTx(ctx, db.GetTxQuery{
			WalletID: f.wallet.ID, Txid: f.reward.Hash,
		})
		require.NoError(t, err)
		require.Equal(t, f.reward, tx)
	})

	t.Run("DisconnectBlock", func(t *testing.T) {
		t.Parallel()

		// Arrange: Use a fresh checkpoint, its actual predecessor and
		// a confirmed credit whose block deletion will fail.
		ctx := t.Context()
		f := setup(t)
		tip := *f.wallet.SyncedTo

		// Act: Deliver the native disconnect with proof of the missing tip.
		err := f.store.DisconnectBlock(ctx, f.wallet.ID, tip, &f.fork,
			map[uint32]*chainhash.Hash{tip.Height: nil})

		// Assert: The delete trigger must have fired, and both snapshots
		// must survive the failed selective disconnect transaction.
		requireDriverConstraintError(t, err, driverRollbackDeleteFailure)
		require.ErrorContains(t, err, "forced rollback block delete failure")
		after, err := f.store.GetWallet(ctx, f.wallet.Name)
		require.NoError(t, err)
		require.Equal(t, f.wallet, after)
		tx, err := f.store.GetTx(ctx, db.GetTxQuery{
			WalletID: f.wallet.ID, Txid: f.reward.Hash,
		})
		require.NoError(t, err)
		require.Equal(t, f.reward, tx)
	})
}

// TestRollbackToBlockRewindsSyncTipAndRollsBack proves the happy path: a
// rollback to a block height both rewinds the wallet sync tip to the fork point
// at height-1 and disconnects the orphaned block's transactions in one
// transaction.
func TestRollbackToBlockRewindsSyncTipAndRollsBack(t *testing.T) {
	t.Parallel()

	// Arrange: Two Wallets retain different hashes at the same height, each
	// with its own confirmed credit, sync tip and birthday reference.
	store := NewTestStore(t)
	walletName := "wallet-rollback-happy"
	walletID := newWallet(t, store, walletName)
	createDerivedAccount(t, store, walletID, db.KeyScopeBIP0084, "default")
	queries := store.Queries()

	addr := newDerivedAddress(
		t, store, walletID, db.KeyScopeBIP0084, "default", false,
	)

	// The fork block at height-1 survives the rollback and becomes the new
	// sync tip.
	forkBlock := CreateBlockFixture(t, queries, walletID, 439)

	confirmedBlock := CreateBlockFixture(t, queries, walletID, 440)
	tx := newRegularTx(
		[]wire.OutPoint{randomOutPoint()},
		[]*wire.TxOut{{Value: 6000, PkScript: addr.ScriptPubKey}},
	)
	err := store.CreateTx(t.Context(), db.CreateTxParams{
		WalletID: walletID,
		Tx:       tx,
		Received: time.Unix(1710004300, 0),
		Block:    &confirmedBlock,
		Status:   db.TxStatusPublished,
		Credits:  map[uint32]address.Address{0: nil},
	})
	require.NoError(t, err)

	err = store.UpdateWallet(t.Context(), db.UpdateWalletParams{
		WalletID:      walletID,
		SyncedTo:      &confirmedBlock,
		BirthdayBlock: &confirmedBlock,
	})
	require.NoError(t, err)

	otherID := newWallet(t, store, "independent-wallet")
	createDerivedAccount(t, store, otherID, db.KeyScopeBIP0084, "default")
	otherAddr := newDerivedAddress(
		t, store, otherID, db.KeyScopeBIP0084, "default", false,
	)
	otherBlock := confirmedBlock
	otherBlock.Hash[0] ^= 1
	require.NoError(t, store.UpdateWallet(t.Context(), db.UpdateWalletParams{
		WalletID: otherID, SyncedTo: &otherBlock, BirthdayBlock: &otherBlock,
	}))

	otherTx := newRegularTx(
		[]wire.OutPoint{randomOutPoint()},
		[]*wire.TxOut{{Value: 7000, PkScript: otherAddr.ScriptPubKey}},
	)
	require.NoError(t, store.CreateTx(t.Context(), db.CreateTxParams{
		WalletID: otherID, Tx: otherTx, Block: &otherBlock,
		Received: time.Unix(1710004300, 0), Status: db.TxStatusPublished,
		Credits: map[uint32]address.Address{0: nil},
	}))
	otherBefore, err := store.GetWallet(t.Context(), "independent-wallet")
	require.NoError(t, err)
	otherTxBefore, err := store.GetTx(t.Context(), db.GetTxQuery{
		WalletID: otherID, Txid: otherTx.TxHash(),
	})
	require.NoError(t, err)
	otherOutputs, err := store.ListUTXOs(t.Context(), db.ListUtxosQuery{
		WalletID: otherID,
	})
	require.NoError(t, err)

	// Act: Rewind A while B still retains its independent chain view.

	err = store.RollbackToBlock(t.Context(), walletID, confirmedBlock.Height)
	require.NoError(t, err)

	// Assert: A rewinds its checkpoint and confirmation, while every
	// observed B record still resolves to its original replacement hash.
	after, err := store.GetWallet(t.Context(), walletName)
	require.NoError(t, err)
	require.NotNil(t, after.SyncedTo)
	require.Equal(t, forkBlock.Height, after.SyncedTo.Height)
	require.Equal(t, forkBlock.Hash, after.SyncedTo.Hash)
	require.Equal(t, after.SyncedTo, after.BirthdayBlock)

	// The previously confirmed transaction is now unmined.
	txInfo, err := store.GetTx(t.Context(), db.GetTxQuery{
		WalletID: walletID,
		Txid:     tx.TxHash(),
	})
	require.NoError(t, err)
	require.Nil(t, txInfo.Block)

	otherAfter, err := store.GetWallet(t.Context(), "independent-wallet")
	require.NoError(t, err)
	require.Equal(t, otherBefore, otherAfter)
	otherTxAfter, err := store.GetTx(t.Context(), db.GetTxQuery{
		WalletID: otherID, Txid: otherTx.TxHash(),
	})
	require.NoError(t, err)
	require.Equal(t, otherTxBefore, otherTxAfter)
	otherOutputsAfter, err := store.ListUTXOs(t.Context(), db.ListUtxosQuery{
		WalletID: otherID,
	})
	require.NoError(t, err)
	require.Equal(t, otherOutputs, otherOutputsAfter)
}

// TestDisconnectBlockIdentities checks the observed-disconnect guard separately
// from height rollback, including a height with no transaction to classify it.
func TestDisconnectBlockIdentities(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		storedHash chainhash.Hash
		forkDelta  byte
		forkHeight uint32
		proof      map[uint32]*chainhash.Hash
		wantRewind bool
		wantErr    error
	}{
		{
			name:       "matching orphan",
			forkHeight: 1,
			storedHash: chainhash.Hash{2},
			proof:      map[uint32]*chainhash.Hash{2: {4}},
			wantRewind: true,
		},
		{
			name:       "stale after replacement with unrelated fork",
			forkDelta:  1,
			forkHeight: 1,
			storedHash: chainhash.Hash{3},
			proof:      map[uint32]*chainhash.Hash{2: {4}},
		},
		{
			name:       "wrong fork hash",
			forkHeight: 1,
			storedHash: chainhash.Hash{2},
			forkDelta:  1,
			proof:      map[uint32]*chainhash.Hash{2: {4}},
			wantErr:    db.ErrBlockMismatch,
		},
		{
			name:       "successor offered as fork",
			forkHeight: 3,
			storedHash: chainhash.Hash{2},
			proof:      map[uint32]*chainhash.Hash{2: {4}},
			wantErr:    db.ErrInvalidParam,
		},
		{
			name:       "unknown empty height",
			forkHeight: 1,
			storedHash: chainhash.Hash{2},
			wantErr:    db.ErrInvalidParam,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Arrange: The observed height has no transactions. Its owned
			// block identity and explicit canonical evidence must decide
			// whether both references rewind, without touching the sibling.
			store := NewTestStore(t)
			walletID := newWallet(t, store, "disconnect-owner")
			fork := CreateBlockFixture(t, store.Queries(), walletID, 1)
			disconnected := db.Block{
				Height:    2,
				Hash:      chainhash.Hash{2},
				Timestamp: time.Unix(2, 0).UTC(),
			}
			stored := disconnected
			stored.Hash = tc.storedHash
			err := store.UpdateWallet(t.Context(), db.UpdateWalletParams{
				WalletID:      walletID,
				SyncedTo:      &stored,
				BirthdayBlock: &stored,
			})
			require.NoError(t, err)
			otherID := newWallet(t, store, "disconnect-sibling")
			otherBlock := disconnected
			otherBlock.Hash = chainhash.Hash{99}
			err = store.UpdateWallet(t.Context(), db.UpdateWalletParams{
				WalletID: otherID, SyncedTo: &otherBlock,
			})
			require.NoError(t, err)
			otherBefore, err := store.GetWallet(
				t.Context(), "disconnect-sibling",
			)
			require.NoError(t, err)

			// Both possible destinations exist. Each case supplies the
			// selected height and may change its hash to model stale proof.
			successor := CreateBlockFixture(t, store.Queries(), walletID, 3)
			destinations := map[uint32]db.Block{1: fork, 3: successor}
			previous := destinations[tc.forkHeight]
			previous.Hash[0] ^= tc.forkDelta

			// Act: Submit the observed block and claimed fork in one write.
			err = store.DisconnectBlock(t.Context(), walletID,
				disconnected, &previous, tc.proof)

			// Assert: Only a proven matching orphan rewinds; both rejected
			// evidence and a delayed old event preserve the installed tip.
			require.ErrorIs(t, err, tc.wantErr)
			current, err := store.GetWallet(
				t.Context(), "disconnect-owner",
			)
			require.NoError(t, err)

			want := stored
			if tc.wantRewind {
				want = fork
			}

			require.Equal(t, want.Height, current.SyncedTo.Height)
			require.Equal(t, want.Hash, current.SyncedTo.Hash)
			require.Equal(t, current.SyncedTo, current.BirthdayBlock)

			// Reading the retained row also detects history deletion hidden
			// behind an otherwise unchanged checkpoint.
			blocks, err := store.ListSyncedBlocks(
				t.Context(), db.ListSyncedBlocksQuery{
					WalletID: walletID, StartHeight: want.Height,
					EndHeight: want.Height,
				},
			)
			require.NoError(t, err)
			require.Len(t, blocks, 1)
			require.Equal(t, want.Hash, blocks[0].Hash)

			otherAfter, err := store.GetWallet(
				t.Context(), "disconnect-sibling",
			)
			require.NoError(t, err)
			require.Equal(t, otherBefore, otherAfter)
		})
	}
}

// TestDisconnectBlockPreservesCanonicalHistory protects live observations above
// a lagging checkpoint from an unrelated later block's disconnection.
func TestDisconnectBlockPreservesCanonicalHistory(t *testing.T) {
	t.Parallel()

	// Arrange: Catch-up is committed only through 419, but a live canonical
	// empty row at 420 and coinbase at 421 are already recorded. The orphan
	// at 422 cannot invalidate that history or demand proof for the empty tail.
	store := NewTestStore(t)
	walletID := newWallet(t, store, "canonical-live-history")
	createDerivedAccount(t, store, walletID, db.KeyScopeBIP0084, "default")
	addr := newDerivedAddress(
		t, store, walletID, db.KeyScopeBIP0084, "default", false,
	)
	fork := CreateBlockFixture(t, store.Queries(), walletID, 419)
	empty := CreateBlockFixture(t, store.Queries(), walletID, 420)
	canonical := CreateBlockFixture(t, store.Queries(), walletID, 421)
	orphan := CreateBlockFixture(t, store.Queries(), walletID, 422)
	coinbase := newCoinbaseTx(addr.ScriptPubKey)
	err := store.CreateTx(t.Context(), db.CreateTxParams{
		WalletID: walletID,
		Tx:       coinbase,
		Block:    &canonical,
		Status:   db.TxStatusPublished,
		Received: time.Unix(1710004200, 0),
		Credits:  map[uint32]address.Address{0: nil},
	})
	require.NoError(t, err)

	spend := newRegularTx(
		[]wire.OutPoint{{Hash: coinbase.TxHash(), Index: 0}},
		[]*wire.TxOut{{Value: 1000, PkScript: addr.ScriptPubKey}},
	)
	err = store.CreateTx(t.Context(), db.CreateTxParams{
		WalletID: walletID,
		Tx:       spend,
		Status:   db.TxStatusPublished,
		Received: time.Unix(1710004201, 0),
		Credits:  map[uint32]address.Address{0: nil},
	})
	require.NoError(t, err)
	err = store.UpdateWallet(t.Context(), db.UpdateWalletParams{
		WalletID: walletID, SyncedTo: &fork,
	})
	require.NoError(t, err)
	coinbaseBefore, err := store.GetTx(t.Context(), db.GetTxQuery{
		WalletID: walletID, Txid: coinbase.TxHash(),
	})
	require.NoError(t, err)
	spendBefore, err := store.GetTx(t.Context(), db.GetTxQuery{
		WalletID: walletID, Txid: spend.TxHash(),
	})
	require.NoError(t, err)

	// Act: The source detached 422; its earlier proven checkpoint fork does
	// not imply that canonical observations between 419 and 421 are abandoned.
	err = store.DisconnectBlock(t.Context(), walletID, orphan, &fork,
		map[uint32]*chainhash.Hash{
			canonical.Height: &canonical.Hash,
			orphan.Height:    nil,
		})

	// Assert: Keep both the valid pending graph and canonical confirmation.
	// The empty orphan disappears without moving the committed checkpoint.
	require.NoError(t, err)
	coinbaseAfter, err := store.GetTx(t.Context(), db.GetTxQuery{
		WalletID: walletID, Txid: coinbase.TxHash(),
	})
	require.NoError(t, err)
	spendAfter, err := store.GetTx(t.Context(), db.GetTxQuery{
		WalletID: walletID, Txid: spend.TxHash(),
	})
	require.NoError(t, err)
	require.Equal(t, spendBefore, spendAfter)
	require.Equal(t, coinbaseBefore, coinbaseAfter)
	current, err := store.GetWallet(t.Context(), "canonical-live-history")
	require.NoError(t, err)
	require.Equal(t, fork.Height, current.SyncedTo.Height)
	require.Equal(t, fork.Hash, current.SyncedTo.Hash)
	retained, err := store.ListSyncedBlocks(
		t.Context(), db.ListSyncedBlocksQuery{
			WalletID:    walletID,
			StartHeight: empty.Height,
			EndHeight:   empty.Height,
		},
	)
	require.NoError(t, err)
	require.Equal(t, empty.Hash, retained[0].Hash)
	_, err = store.ListSyncedBlocks(t.Context(), db.ListSyncedBlocksQuery{
		WalletID:    walletID,
		StartHeight: orphan.Height,
		EndHeight:   orphan.Height,
	})
	require.ErrorIs(t, err, db.ErrBlockNotFound)
}
