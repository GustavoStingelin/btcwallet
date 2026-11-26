-- name: InsertTreeBlock :exec
INSERT INTO tree_blocks (block_hash, block_height, block_timestamp, parent_id)
VALUES (?, ?, ?, ?)
ON CONFLICT (block_hash) DO NOTHING;

-- name: TreeBlockByHash :one
SELECT
    id,
    block_hash,
    block_height,
    block_timestamp,
    parent_id
FROM tree_blocks
WHERE block_hash = ?;

-- name: TreeBlockByID :one
SELECT
    id,
    block_hash,
    block_height,
    block_timestamp,
    parent_id
FROM tree_blocks
WHERE id = ?;

-- name: SetTreeBlockParent :exec
UPDATE tree_blocks SET parent_id = sqlc.arg('parent_id')
WHERE id = sqlc.arg('id') AND parent_id IS NULL;

-- name: TreeCheckpoint :one
SELECT
    b.id,
    b.block_hash,
    b.block_height,
    b.block_timestamp,
    b.parent_id
FROM tree_blocks AS b
INNER JOIN wallet_sync_states AS w ON b.id = w.last_scanned_tree_block_id
WHERE w.wallet_id = ?;

-- name: ListAheadConfirmationHeights :many
SELECT DISTINCT cast(block_height AS INTEGER) AS height
FROM transactions
WHERE wallet_id = cast(sqlc.arg('wallet_id') AS INTEGER) AND block_height > cast(sqlc.arg('floor_height') AS INTEGER);

-- name: DetachTreeConfirmations :exec
UPDATE transactions
SET
    tree_block_id = NULL,
    block_height = NULL,
    confirmed_order = NULL,
    tx_status = CASE WHEN is_coinbase THEN 4 ELSE tx_status END
WHERE wallet_id = sqlc.arg('wallet_id') AND tree_block_id = sqlc.arg('block_id');
