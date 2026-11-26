-- name: CreateWallet :one
INSERT INTO wallets (
    wallet_name,
    is_imported,
    manager_version,
    is_watch_only,
    master_hd_pub_key
) VALUES (
    ?, ?, ?, ?, ?
)
RETURNING id;

-- name: InsertWalletSyncState :exec
INSERT INTO wallet_sync_states (
    wallet_id,
    synced_height,
    birthday_height,
    birthday_timestamp,
    updated_at
) VALUES (
    ?, ?, ?, ?, current_timestamp
);

-- name: UpdateWalletSyncState :execrows
UPDATE wallet_sync_states
SET
    -- If synced_height param is NOT NULL, use it. Otherwise, keep existing value.
    synced_height = coalesce(sqlc.narg('synced_height'), synced_height),
    last_scanned_tree_block_id = coalesce(sqlc.narg('synced_block_id'), last_scanned_tree_block_id),

    -- If birthday_height param is NOT NULL, use it. Otherwise, keep existing value.
    birthday_height = coalesce(sqlc.narg('birthday_height'), birthday_height),
    birthday_block_id = coalesce(sqlc.narg('birthday_block_id'), birthday_block_id),

    -- If birthday_timestamp param is NOT NULL, use it. Otherwise, keep existing value.
    birthday_timestamp = coalesce(sqlc.narg('birthday_timestamp'), birthday_timestamp),

    -- Always update timestamp to current database time.
    updated_at = current_timestamp
WHERE
    wallet_id = sqlc.arg('wallet_id');

-- name: GetWalletByName :one
SELECT
    w.id,
    w.wallet_name,
    w.is_imported,
    w.manager_version,
    w.is_watch_only,
    w.master_hd_pub_key,
    s.synced_height,
    s.birthday_height,
    s.birthday_timestamp,
    s.updated_at,
    b_synced.block_hash AS synced_block_hash,
    b_synced.block_timestamp AS synced_block_timestamp,
    b_birthday.block_hash AS birthday_block_hash,
    b_birthday.block_timestamp AS birthday_block_timestamp
FROM wallets AS w
LEFT JOIN wallet_sync_states AS s ON w.id = s.wallet_id
LEFT JOIN tree_blocks AS b_synced ON s.last_scanned_tree_block_id = b_synced.id
LEFT JOIN tree_blocks AS b_birthday ON s.birthday_block_id = b_birthday.id
WHERE w.wallet_name = ?;

-- name: ListWallets :many
-- Lists wallets using cursor-based pagination. If cursor_id is NULL, starts
-- from the beginning; otherwise returns wallets with id > cursor_id. Returns up
-- to page_limit rows.
SELECT
    w.id,
    w.wallet_name,
    w.is_imported,
    w.manager_version,
    w.is_watch_only,
    w.master_hd_pub_key,
    s.synced_height,
    s.birthday_height,
    s.birthday_timestamp,
    s.updated_at,
    b_synced.block_hash AS synced_block_hash,
    b_synced.block_timestamp AS synced_block_timestamp,
    b_birthday.block_hash AS birthday_block_hash,
    b_birthday.block_timestamp AS birthday_block_timestamp
FROM wallets AS w
LEFT JOIN wallet_sync_states AS s ON w.id = s.wallet_id
LEFT JOIN tree_blocks AS b_synced ON s.last_scanned_tree_block_id = b_synced.id
LEFT JOIN tree_blocks AS b_birthday ON s.birthday_block_id = b_birthday.id
WHERE (
    sqlc.narg('cursor_id') IS NULL
    OR w.id > sqlc.narg('cursor_id')
)
ORDER BY w.id
LIMIT sqlc.arg('page_limit');

-- name: GetWalletByID :one
SELECT
    w.id,
    w.wallet_name,
    w.is_imported,
    w.manager_version,
    w.is_watch_only,
    w.master_hd_pub_key,
    s.synced_height,
    s.birthday_height,
    s.birthday_timestamp,
    s.updated_at,
    b_synced.block_hash AS synced_block_hash,
    b_synced.block_timestamp AS synced_block_timestamp,
    b_birthday.block_hash AS birthday_block_hash,
    b_birthday.block_timestamp AS birthday_block_timestamp
FROM wallets AS w
LEFT JOIN wallet_sync_states AS s ON w.id = s.wallet_id
LEFT JOIN tree_blocks AS b_synced ON s.last_scanned_tree_block_id = b_synced.id
LEFT JOIN tree_blocks AS b_birthday ON s.birthday_block_id = b_birthday.id
WHERE w.id = ?;

-- name: InsertWalletSecrets :exec
INSERT INTO wallet_secrets (
    wallet_id,
    master_priv_params,
    encrypted_crypto_priv_key,
    encrypted_crypto_script_key,
    encrypted_master_hd_priv_key
) VALUES (
    ?, ?, ?, ?, ?
);

-- name: GetWalletSecrets :one
SELECT
    wallet_id,
    master_priv_params,
    encrypted_crypto_priv_key,
    encrypted_crypto_script_key,
    encrypted_master_hd_priv_key
FROM wallet_secrets
WHERE wallet_id = ?;

-- name: UpdateWalletSecrets :execrows
UPDATE wallet_secrets
SET
    master_priv_params = ?,
    encrypted_crypto_priv_key = ?,
    encrypted_crypto_script_key = ?,
    encrypted_master_hd_priv_key = ?
WHERE wallet_id = ?;

-- name: RewindWalletReferencesForDisconnect :execrows
-- Rewind scanned progress across removed history; move birthday only on removal.
UPDATE wallet_sync_states
SET
    synced_height = CASE
        WHEN
            last_scanned_tree_block_id = cast(sqlc.arg('orphan_id') AS INTEGER)
            THEN cast(sqlc.narg('previous_height') AS INTEGER)
        ELSE synced_height
    END,
    last_scanned_tree_block_id = CASE
        WHEN last_scanned_tree_block_id = cast(sqlc.arg('orphan_id') AS INTEGER)
            THEN cast(sqlc.narg('previous_block_id') AS INTEGER)
        ELSE last_scanned_tree_block_id
    END,
    birthday_height = CASE
        WHEN
            birthday_block_id = cast(sqlc.arg('orphan_id') AS INTEGER)
            THEN cast(sqlc.narg('previous_height') AS INTEGER)
        ELSE birthday_height
    END,
    birthday_block_id = CASE
        WHEN birthday_block_id = cast(sqlc.arg('orphan_id') AS INTEGER)
            THEN cast(sqlc.narg('previous_block_id') AS INTEGER)
        ELSE birthday_block_id
    END,
    updated_at = current_timestamp
WHERE
    wallet_id = cast(sqlc.arg('wallet_id') AS INTEGER)
    AND (
        last_scanned_tree_block_id = cast(sqlc.arg('orphan_id') AS INTEGER)
        OR birthday_block_id = cast(sqlc.arg('orphan_id') AS INTEGER)
    );
