//go:build itest

package itest

import (
	"bytes"
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/btcutil/v2"
	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/txscript/v2"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/btcsuite/btcwallet/bwtest"
	"github.com/btcsuite/btcwallet/bwtest/wait"
	"github.com/btcsuite/btcwallet/chain"
	"github.com/btcsuite/btcwallet/waddrmgr"
	"github.com/btcsuite/btcwallet/wallet"
	"github.com/btcsuite/btcwallet/wtxmgr"
	"github.com/stretchr/testify/require"
)

// testWalletSyncRollback verifies that the public tip, transaction and UTXO
// views agree after a real shallow reorg, reconfirmation and Manager reload.
// Each stable state is read once after the exact synchronization boundary;
// retrying a wallet read here would conceal incomplete rollback publication.
func testWalletSyncRollback(h *bwtest.HarnessTest) {
	// Arrange: isolate a second miner before the payment exists, so its
	// winning branch cannot accidentally confirm the payment being detached.
	w, _ := h.NewWallet(bwtest.WalletFixture{})
	_, forkHeight := h.GetBestBlock()
	competitor := h.SpawnTempMiner()

	funding := h.FundWallet(w, oneBTC)
	require.Len(h, funding.WalletOutpoints, 1)
	outpoint := funding.WalletOutpoints[0]
	txid := funding.Tx.TxHash()
	losingHash := funding.Block.BlockHash()
	require.Equal(h, forkHeight+1, funding.BlockHeight)

	var rawTx bytes.Buffer
	require.NoError(h, funding.Tx.Serialize(&rawTx))

	allCoins := wallet.UtxoQuery{MaxConfs: maxConfsLimit}
	confirmedCoins := wallet.UtxoQuery{MinConfs: 1, MaxConfs: maxConfsLimit}

	// The funding helper has crossed the Wallet boundary. Establish a
	// non-empty baseline tied to the actual payment, rather than comparing
	// snapshots that could all agree while omitting it.
	info, err := w.Info(h.Context())
	require.NoError(h, err)
	require.True(h, info.Synced)
	require.Equal(h, losingHash, info.SyncedTo.Hash)
	require.Equal(h, funding.BlockHeight, info.SyncedTo.Height)

	mined, err := w.GetTx(h.Context(), txid)
	require.NoError(h, err)
	require.Equal(h, txid, mined.Hash)
	require.Equal(h, rawTx.Bytes(), mined.RawTx)
	require.Equal(h, btcutil.Amount(oneBTC), mined.Value)
	require.Equal(h, wallet.TxStatusPublished, mined.Status)
	require.Equal(h, &wallet.BlockDetails{
		Hash:      losingHash,
		Height:    funding.BlockHeight,
		Timestamp: funding.Block.Header.Timestamp.Unix(),
	}, mined.Block)
	require.Equal(h, int32(1), mined.Confirmations)

	history, err := w.ListTxns(h.Context(), 0, unminedHeight)
	require.NoError(h, err)
	require.Equal(h, []*wallet.TxDetail{mined}, history)

	coin, err := w.GetUtxo(h.Context(), outpoint)
	require.NoError(h, err)
	require.Equal(h, outpoint, coin.OutPoint)
	require.Equal(h, btcutil.Amount(oneBTC), coin.Amount)
	require.Equal(h, funding.Tx.TxOut[outpoint.Index].PkScript, coin.PkScript)
	require.Equal(h, int32(1), coin.Confirmations)

	coins, err := w.ListUnspent(h.Context(), allCoins)
	require.NoError(h, err)
	require.Equal(h, []*wallet.Utxo{coin}, coins)

	confirmed, err := w.ListUnspent(h.Context(), confirmedCoins)
	require.NoError(h, err)
	require.Equal(h, coins, confirmed)

	// Act: two empty blocks beat the one-block payment branch. Wait for the
	// miner's known winning hash before asking the Wallet to catch up;
	// otherwise synchronization could still succeed at the losing tip.
	winningBlocks := competitor.MineEmptyBlocks(2)
	winningTip := winningBlocks[len(winningBlocks)-1].BlockHash()

	h.ConnectToMiner(competitor)
	h.WaitForMinerTip(winningTip, forkHeight+2)
	h.AssertWalletSynced(w)

	// Assert: rollback detaches an ordinary payment without invalidating it.
	// Its output remains unconfirmed, but must disappear from confirmed-only
	// queries. No mempool delivery to the Wallet is required, including SPV.
	info, err = w.Info(h.Context())
	require.NoError(h, err)
	require.True(h, info.Synced)
	require.Equal(h, winningTip, info.SyncedTo.Hash)
	require.Equal(h, forkHeight+2, info.SyncedTo.Height)

	unmined, err := w.GetTx(h.Context(), txid)
	require.NoError(h, err)
	require.Equal(h, wallet.TxStatusPublished, unmined.Status)
	require.Nil(h, unmined.Block)
	require.Zero(h, unmined.Confirmations)

	wantUnmined := *mined
	wantUnmined.Block = nil
	wantUnmined.Confirmations = 0
	require.Equal(h, &wantUnmined, unmined)

	history, err = w.ListTxns(h.Context(), 0, unminedHeight)
	require.NoError(h, err)
	require.Equal(h, []*wallet.TxDetail{unmined}, history)

	unconfirmedCoin, err := w.GetUtxo(h.Context(), outpoint)
	require.NoError(h, err)

	wantUnconfirmedCoin := *coin
	wantUnconfirmedCoin.Confirmations = 0
	require.Equal(h, &wantUnconfirmedCoin, unconfirmedCoin)

	coins, err = w.ListUnspent(h.Context(), allCoins)
	require.NoError(h, err)
	require.Equal(h, []*wallet.Utxo{unconfirmedCoin}, coins)

	confirmed, err = w.ListUnspent(h.Context(), confirmedCoins)
	require.NoError(h, err)
	require.Empty(h, confirmed)

	// Act: extend the winning branch with the same payment. The miner's
	// mempool wait is an external event; MineBlockWithTx then supplies the
	// exact Wallet synchronization boundary and empties the miner mempool.
	newBlock := h.MineBlockWithTx(funding.Tx)
	newHash := newBlock.BlockHash()

	// Assert: the new block, never the losing block at the same transaction
	// hash, owns confirmation. Transaction receipt times can be refreshed on
	// confirmation by kvdb, so only that transition allows a changed time.
	require.NotEqual(h, losingHash, newHash)
	require.Equal(h, winningTip, newBlock.Header.PrevBlock)
	info, err = w.Info(h.Context())
	require.NoError(h, err)
	require.True(h, info.Synced)
	require.Equal(h, newHash, info.SyncedTo.Hash)
	require.Equal(h, forkHeight+3, info.SyncedTo.Height)

	reconfirmed, err := w.GetTx(h.Context(), txid)
	require.NoError(h, err)
	require.Equal(h, &wallet.BlockDetails{
		Hash:      newHash,
		Height:    forkHeight + 3,
		Timestamp: newBlock.Header.Timestamp.Unix(),
	}, reconfirmed.Block)
	require.Equal(h, int32(1), reconfirmed.Confirmations)
	require.False(h, reconfirmed.ReceivedTime.IsZero())

	wantReconfirmed := *mined
	wantReconfirmed.Block = reconfirmed.Block
	wantReconfirmed.ReceivedTime = reconfirmed.ReceivedTime
	require.Equal(h, &wantReconfirmed, reconfirmed)

	history, err = w.ListTxns(h.Context(), 0, unminedHeight)
	require.NoError(h, err)
	require.Equal(h, []*wallet.TxDetail{reconfirmed}, history)

	reconfirmedCoin, err := w.GetUtxo(h.Context(), outpoint)
	require.NoError(h, err)
	require.Equal(h, coin, reconfirmedCoin)

	coins, err = w.ListUnspent(h.Context(), allCoins)
	require.NoError(h, err)
	require.Equal(h, []*wallet.Utxo{reconfirmedCoin}, coins)

	confirmed, err = w.ListUnspent(h.Context(), confirmedCoins)
	require.NoError(h, err)
	require.Equal(h, coins, confirmed)

	// Act: ReloadWallet stops the old Manager and starts a fresh one over
	// the same database. Only the returned Wallet may serve the final reads.
	id := w.ID()
	w = h.ReloadWallet(w)
	h.AssertWalletSynced(w)

	// Assert: every final public view survives the full durable lifecycle,
	// including metadata that was not allowed to change across reload.
	require.Equal(h, id, w.ID())
	reopenedInfo, err := w.Info(h.Context())
	require.NoError(h, err)
	require.True(h, reopenedInfo.Synced)
	require.Equal(h, info.SyncedTo, reopenedInfo.SyncedTo)

	reopenedTx, err := w.GetTx(h.Context(), txid)
	require.NoError(h, err)
	require.Equal(h, reconfirmed, reopenedTx)

	reopenedHistory, err := w.ListTxns(h.Context(), 0, unminedHeight)
	require.NoError(h, err)
	require.Equal(h, history, reopenedHistory)

	reopenedCoin, err := w.GetUtxo(h.Context(), outpoint)
	require.NoError(h, err)
	require.Equal(h, reconfirmedCoin, reopenedCoin)

	reopenedCoins, err := w.ListUnspent(h.Context(), allCoins)
	require.NoError(h, err)
	require.Equal(h, coins, reopenedCoins)

	reopenedConfirmed, err := w.ListUnspent(h.Context(), confirmedCoins)
	require.NoError(h, err)
	require.Equal(h, confirmed, reopenedConfirmed)
}

// testWalletSyncOfflineRollback proves startup recovery can walk a committed
// branch when the Wallet missed every disconnect while offline.
func testWalletSyncOfflineRollback(h *bwtest.HarnessTest) {
	// Arrange: The losing branch has a payment and an empty block above it.
	w, _ := h.NewWallet(bwtest.WalletFixture{})
	competitor := h.SpawnTempMiner()
	funding := h.FundWallet(w, oneBTC)
	h.MineEmptyBlocks(1)

	txid := funding.Tx.TxHash()
	payment, err := w.GetTx(h.Context(), txid)
	require.NoError(h, err)
	require.Equal(h, int32(2), payment.Confirmations)

	// Act: The winning miner replaces both blocks while the Store is closed.
	w = h.ReloadWalletAfter(w, func() {
		winning := competitor.MineEmptyBlocks(3)
		h.ConnectToMiner(competitor)
		h.WaitForMinerTip(
			winning[len(winning)-1].BlockHash(), funding.BlockHeight+2,
		)
	})
	h.AssertWalletSynced(w)

	// Assert: Startup detached the payment and reached the new chain tip.
	payment, err = w.GetTx(h.Context(), txid)
	require.NoError(h, err)
	require.Nil(h, payment.Block)
	require.Zero(h, payment.Confirmations)

	bestHash, bestHeight := h.GetBestBlock()
	info, err := w.Info(h.Context())
	require.NoError(h, err)
	require.Equal(h, *bestHash, info.SyncedTo.Hash)
	require.Equal(h, bestHeight, info.SyncedTo.Height)

	// Finish with an empty mempool for the next harness case.
	h.MineBlockWithTx(funding.Tx)
}

// sparsePaymentSource holds back scan progress and forwards only the mined
// payment chosen by the test. All headers, filters and blocks still come from
// the real chain backend.
type sparsePaymentSource struct {
	chain.Interface

	events     chan any
	checkpoint *chainhash.Hash
	height     int32
	frozen     atomic.Bool
}

// SubscribeChainEvents delivers only observations explicitly sent by the test.
func (s *sparsePaymentSource) SubscribeChainEvents(
	ctx context.Context) chain.EventSubscription {

	done := make(chan struct{})
	context.AfterFunc(ctx, func() {
		close(s.events)
		close(done)
	})

	return chain.EventSubscription{Events: s.events, Done: done}
}

// GetBestBlock lets the test keep the original worker at its known checkpoint.
func (s *sparsePaymentSource) GetBestBlock() (*chainhash.Hash, int32, error) {
	if s.frozen.Load() {
		return s.checkpoint, s.height, nil
	}

	return s.Interface.GetBestBlock()
}

// waitForSourceTip checks that the node serving wallet reads has caught up
// with the separate miner before the test delivers or reopens wallet work.
func (s *sparsePaymentSource) waitForSourceTip(h *bwtest.HarnessTest,
	hash chainhash.Hash, height int32) {

	h.Helper()

	err := wait.NoError(func() error {
		bestHash, bestHeight, err := s.Interface.GetBestBlock()
		if err != nil {
			return err
		}

		if bestHeight != height || *bestHash != hash {
			return fmt.Errorf("source tip %v at %d, want %v at %d",
				bestHash, bestHeight, hash, height)
		}

		return nil
	}, 10*time.Second)
	require.NoError(h, err)
}

// testWalletSyncSparseRollback checks that a canonical checkpoint does not
// validate a payment observed beyond it on a branch replaced while offline.
func testWalletSyncSparseRollback(h *bwtest.HarnessTest) {
	//nolint:staticcheck // Sparse startup recovery is a SQL Store capability.
	if *dbBackend == string(wallet.DBBackendKVDB) {
		h.Skip("sparse startup recovery requires a SQL backend")
	}

	// Arrange: Deliver the payment two heights ahead of the saved checkpoint,
	// without delivering block progress or the intervening block.
	fork, height := h.GetBestBlock()
	source := &sparsePaymentSource{
		Interface: h.ChainClient, events: make(chan any, 1),
		checkpoint: fork, height: height,
	}
	source.waitForSourceTip(h, *fork, height)
	h.ChainClient = source
	w, _ := h.NewWallet(bwtest.WalletFixture{})
	h.AssertWalletSynced(w)
	source.frozen.Store(true)

	competitor := h.SpawnTempMiner()
	h.GenerateBlocks(1)

	addrs := h.NewWalletAddressesOfType(w, waddrmgr.WitnessPubKey, 1)
	script, err := txscript.PayToAddrScript(addrs[0])
	require.NoError(h, err)
	txid := h.SendOutput(
		wire.NewTxOut(int64(oneBTC), script), bwtest.MinerFeeRate,
	)
	tx := h.AssertTxInMempool(*txid)
	losingHash := h.GenerateBlocks(1)[0]
	source.waitForSourceTip(h, *losingHash, height+2)
	block, err := source.GetBlock(losingHash)
	require.NoError(h, err)
	h.AssertTxInBlock(block, *txid)
	record, err := wtxmgr.NewTxRecordFromMsgTx(tx, block.Header.Timestamp)
	require.NoError(h, err)

	source.events <- chain.RelevantTx{
		TxRecord: record,
		Block: &wtxmgr.BlockMeta{
			Block: wtxmgr.Block{Hash: *losingHash, Height: height + 2},
			Time:  block.Header.Timestamp,
		},
	}

	require.Eventually(h, func() bool {
		payment, err := w.GetTx(h.Context(), *txid)

		return err == nil && payment.Block != nil &&
			payment.Block.Hash == *losingHash
	}, 10*time.Second, wait.PollInterval)
	info, err := w.Info(h.Context())
	require.NoError(h, err)
	require.Equal(h, *fork, info.SyncedTo.Hash)

	// Act: Replace the payment block after shutdown, then reopen the same
	// database against the real tip without delivering any disconnections.
	w = h.ReloadWalletAfter(w, func() {
		winning := competitor.MineEmptyBlocks(3)
		h.ConnectToMiner(competitor)

		tip := winning[2].BlockHash()
		h.WaitForMinerTip(tip, height+3)
		source.waitForSourceTip(h, tip, height+3)
		h.ChainClient = source.Interface
	})
	h.AssertWalletSynced(w)
	payment, err := w.GetTx(h.Context(), *txid)
	require.NoError(h, err)

	// Restore the miner's empty mempool without changing the captured wallet
	// result. No wallet notification is needed for this cleanup.
	h.AssertTxInMempool(*txid)
	h.GenerateBlocks(1)

	// Assert: Startup detached the abandoned confirmation.
	require.Nil(h, payment.Block)
	require.Zero(h, payment.Confirmations)
}
