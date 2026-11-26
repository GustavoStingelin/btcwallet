-- name: GetWalletBlocksInRange :many
-- Recover the Wallet's view from its scanned branch and referenced blocks.
WITH RECURSIVE scanned (id, block_height, header_hash, block_timestamp, parent_id) AS (
    SELECT
        b.id,
        b.block_height,
        b.block_hash,
        b.block_timestamp,
        b.parent_id
    FROM wallet_sync_states AS s
    INNER JOIN tree_blocks AS b ON s.last_scanned_tree_block_id = b.id
    WHERE s.wallet_id = sqlc.arg('wallet_id')::BIGINT
    UNION ALL
    SELECT
        b.id,
        b.block_height,
        b.block_hash,
        b.block_timestamp,
        b.parent_id
    FROM scanned AS child
    INNER JOIN tree_blocks AS b ON child.parent_id = b.id
    WHERE child.block_height > sqlc.arg('start_height')::INTEGER
),

candidates AS (
    SELECT
        id,
        block_height,
        header_hash,
        block_timestamp,
        0 AS priority
    FROM scanned
    UNION ALL
    SELECT
        b.id,
        b.block_height,
        b.block_hash,
        b.block_timestamp,
        1 AS priority
    FROM wallet_sync_states AS s
    INNER JOIN tree_blocks AS b ON s.birthday_block_id = b.id
    WHERE s.wallet_id = sqlc.arg('wallet_id')::BIGINT
    UNION ALL
    SELECT DISTINCT
        b.id,
        b.block_height,
        b.block_hash,
        b.block_timestamp,
        2 AS priority
    FROM transactions AS t
    INNER JOIN tree_blocks AS b ON t.tree_block_id = b.id
    WHERE
        t.wallet_id = sqlc.arg('wallet_id')::BIGINT
        AND t.block_height BETWEEN sqlc.arg('start_height')::INTEGER AND sqlc.arg('end_height')::INTEGER
),

selected AS (
    SELECT
        block_height,
        header_hash,
        block_timestamp,
        row_number() OVER (PARTITION BY block_height ORDER BY priority ASC, id DESC) AS block_rank
    FROM candidates
    WHERE block_height BETWEEN sqlc.arg('start_height')::INTEGER AND sqlc.arg('end_height')::INTEGER
)

SELECT
    block_height::INTEGER AS block_height,
    header_hash::BYTEA AS header_hash,
    block_timestamp::BIGINT AS block_timestamp
FROM selected
WHERE block_rank = 1
ORDER BY block_height;
