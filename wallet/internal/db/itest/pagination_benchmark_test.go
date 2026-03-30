//go:build itest

package itest

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/btcsuite/btcwallet/wallet/internal/db"
	"github.com/btcsuite/btcwallet/wallet/internal/db/page"
	"github.com/stretchr/testify/require"
)

const (
	// paginationBenchmarkBackendPostgres identifies PostgreSQL benchmarks.
	paginationBenchmarkBackendPostgres = "postgres"

	// paginationBenchmarkBackendSQLite identifies SQLite benchmarks.
	paginationBenchmarkBackendSQLite = "sqlite"

	// paginationBenchmarkDefaultWalletPrefix names seeded benchmark wallets.
	paginationBenchmarkDefaultWalletPrefix = "pagination-benchmark-wallet"

	// paginationBenchmarkDefaultAccountName names the seeded benchmark account.
	paginationBenchmarkDefaultAccountName = "pagination-benchmark-account"

	// paginationBenchmarkFirstPageName sorts first-page cases before next-page
	// cases in benchmark output.
	paginationBenchmarkFirstPageName = "0-FirstPage"

	// paginationBenchmarkNextPageName sorts next-page cases after first-page
	// cases in benchmark output.
	paginationBenchmarkNextPageName = "1-NextPage"

	paginationBenchmarkSplitVariantName = "0-Split"

	paginationBenchmarkUnifiedVariantName = "1-Unified"
)

var (
	// paginationBenchmarkDatasetSizes defines the shared dataset sizes used by
	// pagination benchmarks.
	paginationBenchmarkDatasetSizes = []int{10, 100, 1000, 10000}

	paginationBenchmarkPageSizes = []uint32{5, 100, 500}

	// paginationBenchmarkDatasetSizePadding keeps benchmark names aligned across
	// dataset sizes for benchstat output.
	paginationBenchmarkDatasetSizePadding = decimalWidth(
		paginationBenchmarkDatasetSizes[len(paginationBenchmarkDatasetSizes)-1],
	)

	paginationBenchmarkPageSizePadding = decimalWidth(
		int(paginationBenchmarkPageSizes[len(paginationBenchmarkPageSizes)-1]),
	)
)

// paginationBenchmarkStore defines the itest store surface the pagination
// benchmarks need for seeding and query-plan capture.
type paginationBenchmarkStore interface {
	db.WalletStore
	db.AccountStore
	db.AddressStore

	DB() *sql.DB
}

// paginationBenchmarkWalletSeedConfig controls deterministic wallet seeding.
type paginationBenchmarkWalletSeedConfig struct {
	// NamePrefix is prefixed to the zero-padded wallet number.
	NamePrefix string

	// Count is the number of wallets to create.
	Count int
}

// paginationBenchmarkAddressSeedConfig controls deterministic address seeding.
type paginationBenchmarkAddressSeedConfig struct {
	// WalletName is the wallet that owns the benchmark account.
	WalletName string

	// Scope is the key scope used for the benchmark account.
	Scope db.KeyScope

	// AccountName is the benchmark account name.
	AccountName string

	// Change selects the internal branch when true.
	Change bool

	// Count is the number of derived addresses to create.
	Count int
}

// paginationBenchmarkAddressSeed captures the seeded account context needed by
// address pagination benchmarks.
type paginationBenchmarkAddressSeed struct {
	WalletID uint32

	Scope db.KeyScope

	AccountName string

	Addresses []db.AddressInfo
}

type paginationBenchmarkQueryVariant string

const (
	paginationBenchmarkQueryVariantSplit paginationBenchmarkQueryVariant = "split"

	paginationBenchmarkQueryVariantUnified paginationBenchmarkQueryVariant = "unified"
)

type paginationBenchmarkSQLQuery struct {
	Variant paginationBenchmarkQueryVariant

	SQL string

	Args []any
}

// paginationBenchmarkQueryPlan captures the raw backend EXPLAIN output for a
// benchmark query.
type paginationBenchmarkQueryPlan struct {
	Backend string

	Variant paginationBenchmarkQueryVariant

	Query string

	Explain string

	Args []any

	Lines []string
}

type paginationBenchmarkQueryResult struct {
	RowCount int

	LastCursor *uint32
}

// Text joins raw EXPLAIN lines into one report-friendly block.
func (p paginationBenchmarkQueryPlan) Text() string {
	return strings.Join(p.Lines, "\n")
}

// paginationBenchmarkDatasetName returns a zero-padded dataset label suitable
// for stable benchmark grouping.
func paginationBenchmarkDatasetName(size int, noun string) string {
	return fmt.Sprintf("%0*d-%s",
		paginationBenchmarkDatasetSizePadding, size, noun,
	)
}

// paginationBenchmarkPageCaseName returns the shared benchmark page label.
func paginationBenchmarkPageCaseName(firstPage bool) string {
	if firstPage {
		return paginationBenchmarkFirstPageName
	}

	return paginationBenchmarkNextPageName
}

func paginationBenchmarkPageSizeName(size uint32) string {
	return fmt.Sprintf("%0*d-PageSize",
		paginationBenchmarkPageSizePadding, size,
	)
}

func paginationBenchmarkHasNextPage(datasetSize int, pageSize uint32) bool {
	return datasetSize > int(pageSize)
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}

	return b
}

func paginationBenchmarkVariantCaseName(
	variant paginationBenchmarkQueryVariant) string {

	switch variant {
	case paginationBenchmarkQueryVariantSplit:
		return paginationBenchmarkSplitVariantName

	case paginationBenchmarkQueryVariantUnified:
		return paginationBenchmarkUnifiedVariantName
	}

	return string(variant)
}

func paginationBenchmarkScenarioCaseName(
	firstPage bool, variant paginationBenchmarkQueryVariant) string {

	return paginationBenchmarkPageCaseName(firstPage) + "/" +
		paginationBenchmarkVariantCaseName(variant)
}

// seedPaginationBenchmarkWallets creates a deterministic wallet set for shared
// pagination benchmarks.
func seedPaginationBenchmarkWallets(tb testing.TB, store db.WalletStore,
	cfg paginationBenchmarkWalletSeedConfig) []db.WalletInfo {

	tb.Helper()

	if cfg.Count < 0 {
		tb.Fatalf("wallet benchmark count must be non-negative: %d", cfg.Count)
	}

	prefix := cfg.NamePrefix
	if prefix == "" {
		prefix = paginationBenchmarkDefaultWalletPrefix
	}

	padding := decimalWidth(maxInt(cfg.Count, 1))
	wallets := make([]db.WalletInfo, 0, cfg.Count)
	for i := 0; i < cfg.Count; i++ {
		walletName := fmt.Sprintf("%s-%0*d", prefix, padding, i+1)

		walletInfo, err := store.CreateWallet(
			tb.Context(), CreateWalletParamsFixture(walletName),
		)
		require.NoError(tb, err)

		wallets = append(wallets, *walletInfo)
	}

	return wallets
}

func seedPaginationBenchmarkAddresses(tb testing.TB,
	store paginationBenchmarkStore,
	cfg paginationBenchmarkAddressSeedConfig) paginationBenchmarkAddressSeed {

	tb.Helper()

	if cfg.Count < 0 {
		tb.Fatalf("address benchmark count must be non-negative: %d", cfg.Count)
	}

	walletName := cfg.WalletName
	if walletName == "" {
		walletName = paginationBenchmarkDefaultWalletPrefix
	}

	scope := cfg.Scope
	if scope == (db.KeyScope{}) {
		scope = db.KeyScopeBIP0084
	}

	accountName := cfg.AccountName
	if accountName == "" {
		accountName = paginationBenchmarkDefaultAccountName
	}

	walletID := createPaginationBenchmarkWallet(tb, store, walletName)
	createPaginationBenchmarkDerivedAccount(
		tb, store, walletID, scope, accountName,
	)

	addresses := createPaginationBenchmarkDerivedAddresses(
		tb, store, walletID, scope, accountName, cfg.Change, cfg.Count,
	)

	return paginationBenchmarkAddressSeed{
		WalletID:    walletID,
		Scope:       scope,
		AccountName: accountName,
		Addresses:   addresses,
	}
}

// paginationBenchmarkFirstPageRequest builds a first-page request with shared
// benchmark defaults.
func paginationBenchmarkFirstPageRequest(pageSize uint32,
	earlyExhaustion bool) page.Request[uint32] {

	req := page.Request[uint32]{}.WithSize(pageSize)
	if earlyExhaustion {
		req = req.WithEarlyExhaustion()
	}

	return req
}

// paginationBenchmarkNextPageRequest builds a next-page request from a cursor
// while preserving the benchmark page-size mode.
func paginationBenchmarkNextPageRequest(pageSize uint32, cursor uint32,
	earlyExhaustion bool) page.Request[uint32] {

	return paginationBenchmarkFirstPageRequest(
		pageSize, earlyExhaustion,
	).WithCursor(cursor)
}

// paginationBenchmarkWalletFirstPageQuery builds a first-page wallet query for
// shared benchmarks.
func paginationBenchmarkWalletFirstPageQuery(pageSize uint32,
	earlyExhaustion bool) db.ListWalletsQuery {

	return db.ListWalletsQuery{
		Page: paginationBenchmarkFirstPageRequest(
			pageSize, earlyExhaustion,
		),
	}
}

// paginationBenchmarkWalletNextPageQuery builds a next-page wallet query for
// shared benchmarks.
func paginationBenchmarkWalletNextPageQuery(pageSize uint32, cursor uint32,
	earlyExhaustion bool) db.ListWalletsQuery {

	return db.ListWalletsQuery{
		Page: paginationBenchmarkNextPageRequest(
			pageSize, cursor, earlyExhaustion,
		),
	}
}

// paginationBenchmarkAddressFirstPageQuery builds a first-page address query
// for shared benchmarks.
func paginationBenchmarkAddressFirstPageQuery(walletID uint32,
	scope db.KeyScope, accountName string, pageSize uint32,
	earlyExhaustion bool) db.ListAddressesQuery {

	return db.ListAddressesQuery{
		WalletID:    walletID,
		Scope:       scope,
		AccountName: accountName,
		Page: paginationBenchmarkFirstPageRequest(
			pageSize, earlyExhaustion,
		),
	}
}

// paginationBenchmarkAddressNextPageQuery builds a next-page address query for
// shared benchmarks.
func paginationBenchmarkAddressNextPageQuery(walletID uint32,
	scope db.KeyScope, accountName string, pageSize uint32, cursor uint32,
	earlyExhaustion bool) db.ListAddressesQuery {

	return db.ListAddressesQuery{
		WalletID:    walletID,
		Scope:       scope,
		AccountName: accountName,
		Page: paginationBenchmarkNextPageRequest(
			pageSize, cursor, earlyExhaustion,
		),
	}
}

// captureWalletPaginationQueryPlan captures the backend query plan for the
// wallet pagination query shape selected by the request cursor.
func captureWalletPaginationQueryPlan(tb testing.TB,
	store paginationBenchmarkStore,
	query db.ListWalletsQuery) paginationBenchmarkQueryPlan {

	return captureWalletPaginationVariantQueryPlan(
		tb, store, paginationBenchmarkQueryVariantSplit, query,
	)
}

func captureWalletPaginationVariantQueryPlan(tb testing.TB,
	store paginationBenchmarkStore, variant paginationBenchmarkQueryVariant,
	query db.ListWalletsQuery) paginationBenchmarkQueryPlan {

	tb.Helper()

	backend := mustPaginationBenchmarkBackend(tb, store)
	targetQuery := walletPaginationBenchmarkQuery(backend, variant, query)

	return capturePaginationBenchmarkQueryPlan(
		tb, store.DB(), backend, targetQuery,
	)
}

// captureAddressPaginationQueryPlan captures the backend query plan for the
// address pagination query shape selected by the request cursor.
func captureAddressPaginationQueryPlan(tb testing.TB,
	store paginationBenchmarkStore,
	query db.ListAddressesQuery) paginationBenchmarkQueryPlan {

	return captureAddressPaginationVariantQueryPlan(
		tb, store, paginationBenchmarkQueryVariantSplit, query,
	)
}

func captureAddressPaginationVariantQueryPlan(tb testing.TB,
	store paginationBenchmarkStore, variant paginationBenchmarkQueryVariant,
	query db.ListAddressesQuery) paginationBenchmarkQueryPlan {

	tb.Helper()

	backend := mustPaginationBenchmarkBackend(tb, store)
	targetQuery := addressPaginationBenchmarkQuery(backend, variant, query)

	return capturePaginationBenchmarkQueryPlan(
		tb, store.DB(), backend, targetQuery,
	)
}

// capturePaginationBenchmarkQueryPlan runs the backend-specific EXPLAIN command
// and returns the raw plan lines.
func capturePaginationBenchmarkQueryPlan(tb testing.TB, dbConn *sql.DB,
	backend string,
	targetQuery paginationBenchmarkSQLQuery) paginationBenchmarkQueryPlan {

	tb.Helper()

	explainQuery := explainQueryPrefix(backend) + targetQuery.SQL
	rows, err := dbConn.QueryContext(
		tb.Context(), explainQuery, targetQuery.Args...,
	)
	require.NoError(tb, err)
	defer rows.Close()

	plan := paginationBenchmarkQueryPlan{
		Backend: backend,
		Variant: targetQuery.Variant,
		Query:   targetQuery.SQL,
		Explain: explainQuery,
		Args:    append([]any(nil), targetQuery.Args...),
		Lines:   make([]string, 0),
	}

	switch backend {
	case paginationBenchmarkBackendPostgres:
		for rows.Next() {
			var line string

			err = rows.Scan(&line)
			require.NoError(tb, err)

			plan.Lines = append(plan.Lines, line)
		}

	case paginationBenchmarkBackendSQLite:
		for rows.Next() {
			var id int64
			var parent int64
			var notUsed int64
			var detail string

			err = rows.Scan(&id, &parent, &notUsed, &detail)
			require.NoError(tb, err)

			plan.Lines = append(plan.Lines, fmt.Sprintf(
				"%d|%d|%d|%s", id, parent, notUsed, detail,
			))
		}
	}

	require.NoError(tb, rows.Err())

	return plan
}

func runPaginationBenchmarkQuery(tb testing.TB, dbConn *sql.DB,
	targetQuery paginationBenchmarkSQLQuery) paginationBenchmarkQueryResult {

	tb.Helper()

	rows, err := dbConn.QueryContext(
		tb.Context(), targetQuery.SQL, targetQuery.Args...,
	)
	require.NoError(tb, err)
	defer rows.Close()

	columns, err := rows.Columns()
	require.NoError(tb, err)
	require.NotEmpty(tb, columns)

	values := make([]any, len(columns))
	scanArgs := make([]any, len(columns))
	for i := range values {
		scanArgs[i] = &values[i]
	}

	result := paginationBenchmarkQueryResult{}
	var lastCursor uint32
	for rows.Next() {
		err = rows.Scan(scanArgs...)
		require.NoError(tb, err)

		lastCursor = paginationBenchmarkUint32Value(tb, values[0])
		result.RowCount++
	}

	require.NoError(tb, rows.Err())

	if result.RowCount > 0 {
		result.LastCursor = &lastCursor
	}

	return result
}

func paginationBenchmarkUint32Value(tb testing.TB, value any) uint32 {
	tb.Helper()

	switch typed := value.(type) {
	case int64:
		return uint32(typed)

	case int32:
		return uint32(typed)

	case int:
		return uint32(typed)

	case uint64:
		return uint32(typed)

	case uint32:
		return typed

	case []byte:
		return paginationBenchmarkUint32TextValue(tb, string(typed))

	case string:
		return paginationBenchmarkUint32TextValue(tb, typed)
	}

	tb.Fatalf("unsupported pagination benchmark cursor type: %T", value)

	return 0
}

func paginationBenchmarkUint32TextValue(tb testing.TB, value string) uint32 {
	tb.Helper()

	parsed, err := strconv.ParseUint(value, 10, 32)
	require.NoError(tb, err)

	return uint32(parsed)
}

func walletPaginationBenchmarkQuery(backend string,
	variant paginationBenchmarkQueryVariant,
	query db.ListWalletsQuery) paginationBenchmarkSQLQuery {

	pageLimit := int64(query.Page.QueryLimit())
	switch variant {
	case paginationBenchmarkQueryVariantUnified:
		return paginationBenchmarkSQLQuery{
			Variant: variant,
			SQL:     walletUnifiedPageQuery(backend),
			Args: []any{
				paginationBenchmarkOptionalCursorArg(
					query.Page.Cursor(),
				),
				pageLimit,
			},
		}
	}

	if query.Page.Cursor() == nil {
		return paginationBenchmarkSQLQuery{
			Variant: variant,
			SQL:     walletFirstPageQuery(backend),
			Args:    []any{pageLimit},
		}
	}

	return paginationBenchmarkSQLQuery{
		Variant: variant,
		SQL:     walletNextPageQuery(backend),
		Args: []any{
			int64(*query.Page.Cursor()),
			pageLimit,
		},
	}
}

func addressPaginationBenchmarkQuery(backend string,
	variant paginationBenchmarkQueryVariant,
	query db.ListAddressesQuery) paginationBenchmarkSQLQuery {

	args := []any{
		int64(query.WalletID),
		int64(query.Scope.Purpose),
		int64(query.Scope.Coin),
		query.AccountName,
	}

	pageLimit := int64(query.Page.QueryLimit())
	switch variant {
	case paginationBenchmarkQueryVariantUnified:
		return paginationBenchmarkSQLQuery{
			Variant: variant,
			SQL:     addressUnifiedPageQuery(backend),
			Args: append(
				args,
				paginationBenchmarkOptionalCursorArg(
					query.Page.Cursor(),
				),
				pageLimit,
			),
		}
	}

	if query.Page.Cursor() == nil {
		return paginationBenchmarkSQLQuery{
			Variant: variant,
			SQL:     addressFirstPageQuery(backend),
			Args:    append(args, pageLimit),
		}
	}

	args = append(args, int64(*query.Page.Cursor()), pageLimit)

	return paginationBenchmarkSQLQuery{
		Variant: variant,
		SQL:     addressNextPageQuery(backend),
		Args:    args,
	}
}

func paginationBenchmarkOptionalCursorArg(cursor *uint32) any {
	if cursor == nil {
		return nil
	}

	return int64(*cursor)
}

// mustPaginationBenchmarkBackend resolves the current benchmark backend from the
// concrete store type.
func mustPaginationBenchmarkBackend(tb testing.TB, store any) string {
	tb.Helper()

	switch store.(type) {
	case *db.PostgresStore:
		return paginationBenchmarkBackendPostgres

	case *db.SqliteStore:
		return paginationBenchmarkBackendSQLite
	}

	tb.Fatalf("unsupported pagination benchmark store type: %T", store)

	return ""
}

// explainQueryPrefix returns the backend-specific EXPLAIN prefix.
func explainQueryPrefix(backend string) string {
	switch backend {
	case paginationBenchmarkBackendPostgres:
		return "EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) "

	case paginationBenchmarkBackendSQLite:
		return "EXPLAIN QUERY PLAN "
	}

	return ""
}

func joinPaginationBenchmarkSQL(lines ...string) string {
	return strings.Join(lines, "\n")
}

// walletFirstPageQuery returns the first-page wallet SQL for the selected
// backend.
func walletFirstPageQuery(backend string) string {
	switch backend {
	case paginationBenchmarkBackendPostgres:
		return joinPaginationBenchmarkSQL(
			"SELECT",
			"    w.id,",
			"    w.wallet_name,",
			"    w.is_imported,",
			"    w.manager_version,",
			"    w.is_watch_only,",
			"    s.synced_height,",
			"    s.birthday_height,",
			"    s.birthday_timestamp,",
			"    s.updated_at,",
			"    b_synced.header_hash AS synced_block_hash,",
			"    b_synced.block_timestamp AS",
			"        synced_block_timestamp,",
			"    b_birthday.header_hash AS birthday_block_hash,",
			"    b_birthday.block_timestamp AS",
			"        birthday_block_timestamp",
			"FROM wallets AS w",
			"LEFT JOIN wallet_sync_states AS s ON w.id = s.wallet_id",
			"LEFT JOIN blocks AS b_synced ON",
			"    s.synced_height = b_synced.block_height",
			"LEFT JOIN blocks AS b_birthday ON",
			"    s.birthday_height = b_birthday.block_height",
			"ORDER BY w.id",
			"LIMIT $1;",
		)

	case paginationBenchmarkBackendSQLite:
		return joinPaginationBenchmarkSQL(
			"SELECT",
			"    w.id,",
			"    w.wallet_name,",
			"    w.is_imported,",
			"    w.manager_version,",
			"    w.is_watch_only,",
			"    s.synced_height,",
			"    s.birthday_height,",
			"    s.birthday_timestamp,",
			"    s.updated_at,",
			"    b_synced.header_hash AS synced_block_hash,",
			"    b_synced.block_timestamp AS",
			"        synced_block_timestamp,",
			"    b_birthday.header_hash AS birthday_block_hash,",
			"    b_birthday.block_timestamp AS",
			"        birthday_block_timestamp",
			"FROM wallets AS w",
			"LEFT JOIN wallet_sync_states AS s ON w.id = s.wallet_id",
			"LEFT JOIN blocks AS b_synced ON",
			"    s.synced_height = b_synced.block_height",
			"LEFT JOIN blocks AS b_birthday ON",
			"    s.birthday_height = b_birthday.block_height",
			"ORDER BY w.id",
			"LIMIT ?;",
		)
	}

	return ""
}

// walletNextPageQuery returns the next-page wallet SQL for the selected
// backend.
func walletNextPageQuery(backend string) string {
	switch backend {
	case paginationBenchmarkBackendPostgres:
		return joinPaginationBenchmarkSQL(
			"SELECT",
			"    w.id,",
			"    w.wallet_name,",
			"    w.is_imported,",
			"    w.manager_version,",
			"    w.is_watch_only,",
			"    s.synced_height,",
			"    s.birthday_height,",
			"    s.birthday_timestamp,",
			"    s.updated_at,",
			"    b_synced.header_hash AS synced_block_hash,",
			"    b_synced.block_timestamp AS",
			"        synced_block_timestamp,",
			"    b_birthday.header_hash AS birthday_block_hash,",
			"    b_birthday.block_timestamp AS",
			"        birthday_block_timestamp",
			"FROM wallets AS w",
			"LEFT JOIN wallet_sync_states AS s ON w.id = s.wallet_id",
			"LEFT JOIN blocks AS b_synced ON",
			"    s.synced_height = b_synced.block_height",
			"LEFT JOIN blocks AS b_birthday ON",
			"    s.birthday_height = b_birthday.block_height",
			"WHERE w.id > $1",
			"ORDER BY w.id",
			"LIMIT $2;",
		)

	case paginationBenchmarkBackendSQLite:
		return joinPaginationBenchmarkSQL(
			"SELECT",
			"    w.id,",
			"    w.wallet_name,",
			"    w.is_imported,",
			"    w.manager_version,",
			"    w.is_watch_only,",
			"    s.synced_height,",
			"    s.birthday_height,",
			"    s.birthday_timestamp,",
			"    s.updated_at,",
			"    b_synced.header_hash AS synced_block_hash,",
			"    b_synced.block_timestamp AS",
			"        synced_block_timestamp,",
			"    b_birthday.header_hash AS birthday_block_hash,",
			"    b_birthday.block_timestamp AS",
			"        birthday_block_timestamp",
			"FROM wallets AS w",
			"LEFT JOIN wallet_sync_states AS s ON w.id = s.wallet_id",
			"LEFT JOIN blocks AS b_synced ON",
			"    s.synced_height = b_synced.block_height",
			"LEFT JOIN blocks AS b_birthday ON",
			"    s.birthday_height = b_birthday.block_height",
			"WHERE w.id > ?",
			"ORDER BY w.id",
			"LIMIT ?;",
		)
	}

	return ""
}

func walletUnifiedPageQuery(backend string) string {
	switch backend {
	case paginationBenchmarkBackendPostgres:
		return joinPaginationBenchmarkSQL(
			"SELECT",
			"    w.id,",
			"    w.wallet_name,",
			"    w.is_imported,",
			"    w.manager_version,",
			"    w.is_watch_only,",
			"    s.synced_height,",
			"    s.birthday_height,",
			"    s.birthday_timestamp,",
			"    s.updated_at,",
			"    b_synced.header_hash AS synced_block_hash,",
			"    b_synced.block_timestamp AS",
			"        synced_block_timestamp,",
			"    b_birthday.header_hash AS birthday_block_hash,",
			"    b_birthday.block_timestamp AS",
			"        birthday_block_timestamp",
			"FROM wallets AS w",
			"LEFT JOIN wallet_sync_states AS s ON w.id = s.wallet_id",
			"LEFT JOIN blocks AS b_synced ON",
			"    s.synced_height = b_synced.block_height",
			"LEFT JOIN blocks AS b_birthday ON",
			"    s.birthday_height = b_birthday.block_height",
			"WHERE",
			"    $1::BIGINT IS NULL OR w.id > $1::BIGINT",
			"ORDER BY w.id",
			"LIMIT $2::BIGINT;",
		)

	case paginationBenchmarkBackendSQLite:
		return joinPaginationBenchmarkSQL(
			"SELECT",
			"    w.id,",
			"    w.wallet_name,",
			"    w.is_imported,",
			"    w.manager_version,",
			"    w.is_watch_only,",
			"    s.synced_height,",
			"    s.birthday_height,",
			"    s.birthday_timestamp,",
			"    s.updated_at,",
			"    b_synced.header_hash AS synced_block_hash,",
			"    b_synced.block_timestamp AS",
			"        synced_block_timestamp,",
			"    b_birthday.header_hash AS birthday_block_hash,",
			"    b_birthday.block_timestamp AS",
			"        birthday_block_timestamp",
			"FROM wallets AS w",
			"LEFT JOIN wallet_sync_states AS s ON w.id = s.wallet_id",
			"LEFT JOIN blocks AS b_synced ON",
			"    s.synced_height = b_synced.block_height",
			"LEFT JOIN blocks AS b_birthday ON",
			"    s.birthday_height = b_birthday.block_height",
			"WHERE ?1 IS NULL OR w.id > ?1",
			"ORDER BY w.id",
			"LIMIT ?2;",
		)
	}

	return ""
}

// addressFirstPageQuery returns the first-page address SQL for the selected
// backend.
func addressFirstPageQuery(backend string) string {
	switch backend {
	case paginationBenchmarkBackendPostgres:
		return joinPaginationBenchmarkSQL(
			"SELECT",
			"    a.id,",
			"    a.account_id,",
			"    a.type_id,",
			"    a.address_branch,",
			"    a.address_index,",
			"    a.script_pub_key,",
			"    a.pub_key,",
			"    a.created_at,",
			"    acc.origin_id,",
			"    (s.encrypted_priv_key IS NOT NULL)::BOOLEAN AS",
			"        has_private_key,",
			"    (s.encrypted_script IS NOT NULL)::BOOLEAN AS has_script",
			"FROM addresses AS a",
			"INNER JOIN accounts AS acc ON a.account_id = acc.id",
			"INNER JOIN key_scopes AS ks ON acc.scope_id = ks.id",
			"LEFT JOIN address_secrets AS s ON a.id = s.address_id",
			"WHERE",
			"    ks.wallet_id = $1 AND ks.purpose = $2 AND",
			"    ks.coin_type = $3 AND acc.account_name = $4",
			"ORDER BY a.id",
			"LIMIT $5;",
		)

	case paginationBenchmarkBackendSQLite:
		return joinPaginationBenchmarkSQL(
			"SELECT",
			"    a.id,",
			"    a.account_id,",
			"    a.type_id,",
			"    a.address_branch,",
			"    a.address_index,",
			"    a.script_pub_key,",
			"    a.pub_key,",
			"    a.created_at,",
			"    acc.origin_id,",
			"    s.encrypted_priv_key IS NOT NULL AS has_private_key,",
			"    s.encrypted_script IS NOT NULL AS has_script",
			"FROM addresses AS a",
			"INNER JOIN accounts AS acc ON a.account_id = acc.id",
			"INNER JOIN key_scopes AS ks ON acc.scope_id = ks.id",
			"LEFT JOIN address_secrets AS s ON a.id = s.address_id",
			"WHERE",
			"    ks.wallet_id = ? AND ks.purpose = ? AND",
			"    ks.coin_type = ? AND acc.account_name = ?",
			"ORDER BY a.id",
			"LIMIT ?;",
		)
	}

	return ""
}

// addressNextPageQuery returns the next-page address SQL for the selected
// backend.
func addressNextPageQuery(backend string) string {
	switch backend {
	case paginationBenchmarkBackendPostgres:
		return joinPaginationBenchmarkSQL(
			"SELECT",
			"    a.id,",
			"    a.account_id,",
			"    a.type_id,",
			"    a.address_branch,",
			"    a.address_index,",
			"    a.script_pub_key,",
			"    a.pub_key,",
			"    a.created_at,",
			"    acc.origin_id,",
			"    (s.encrypted_priv_key IS NOT NULL)::BOOLEAN AS",
			"        has_private_key,",
			"    (s.encrypted_script IS NOT NULL)::BOOLEAN AS has_script",
			"FROM addresses AS a",
			"INNER JOIN accounts AS acc ON a.account_id = acc.id",
			"INNER JOIN key_scopes AS ks ON acc.scope_id = ks.id",
			"LEFT JOIN address_secrets AS s ON a.id = s.address_id",
			"WHERE",
			"    ks.wallet_id = $1 AND ks.purpose = $2 AND",
			"    ks.coin_type = $3 AND acc.account_name = $4 AND",
			"    a.id > $5",
			"ORDER BY a.id",
			"LIMIT $6;",
		)

	case paginationBenchmarkBackendSQLite:
		return joinPaginationBenchmarkSQL(
			"SELECT",
			"    a.id,",
			"    a.account_id,",
			"    a.type_id,",
			"    a.address_branch,",
			"    a.address_index,",
			"    a.script_pub_key,",
			"    a.pub_key,",
			"    a.created_at,",
			"    acc.origin_id,",
			"    s.encrypted_priv_key IS NOT NULL AS has_private_key,",
			"    s.encrypted_script IS NOT NULL AS has_script",
			"FROM addresses AS a",
			"INNER JOIN accounts AS acc ON a.account_id = acc.id",
			"INNER JOIN key_scopes AS ks ON acc.scope_id = ks.id",
			"LEFT JOIN address_secrets AS s ON a.id = s.address_id",
			"WHERE",
			"    ks.wallet_id = ? AND ks.purpose = ? AND",
			"    ks.coin_type = ? AND acc.account_name = ? AND a.id > ?",
			"ORDER BY a.id",
			"LIMIT ?;",
		)
	}

	return ""
}

func addressUnifiedPageQuery(backend string) string {
	switch backend {
	case paginationBenchmarkBackendPostgres:
		return joinPaginationBenchmarkSQL(
			"SELECT",
			"    a.id,",
			"    a.account_id,",
			"    a.type_id,",
			"    a.address_branch,",
			"    a.address_index,",
			"    a.script_pub_key,",
			"    a.pub_key,",
			"    a.created_at,",
			"    acc.origin_id,",
			"    (s.encrypted_priv_key IS NOT NULL)::BOOLEAN AS",
			"        has_private_key,",
			"    (s.encrypted_script IS NOT NULL)::BOOLEAN AS has_script",
			"FROM addresses AS a",
			"INNER JOIN accounts AS acc ON a.account_id = acc.id",
			"INNER JOIN key_scopes AS ks ON acc.scope_id = ks.id",
			"LEFT JOIN address_secrets AS s ON a.id = s.address_id",
			"WHERE",
			"    ks.wallet_id = $1 AND ks.purpose = $2 AND",
			"    ks.coin_type = $3 AND acc.account_name = $4 AND",
			"    ($5::BIGINT IS NULL OR a.id > $5::BIGINT)",
			"ORDER BY a.id",
			"LIMIT $6::BIGINT;",
		)

	case paginationBenchmarkBackendSQLite:
		return joinPaginationBenchmarkSQL(
			"SELECT",
			"    a.id,",
			"    a.account_id,",
			"    a.type_id,",
			"    a.address_branch,",
			"    a.address_index,",
			"    a.script_pub_key,",
			"    a.pub_key,",
			"    a.created_at,",
			"    acc.origin_id,",
			"    s.encrypted_priv_key IS NOT NULL AS has_private_key,",
			"    s.encrypted_script IS NOT NULL AS has_script",
			"FROM addresses AS a",
			"INNER JOIN accounts AS acc ON a.account_id = acc.id",
			"INNER JOIN key_scopes AS ks ON acc.scope_id = ks.id",
			"LEFT JOIN address_secrets AS s ON a.id = s.address_id",
			"WHERE",
			"    ks.wallet_id = ?1 AND ks.purpose = ?2 AND",
			"    ks.coin_type = ?3 AND acc.account_name = ?4 AND",
			"    (?5 IS NULL OR a.id > ?5)",
			"ORDER BY a.id",
			"LIMIT ?6;",
		)
	}

	return ""
}

// createPaginationBenchmarkWallet creates one deterministic wallet.
func createPaginationBenchmarkWallet(tb testing.TB, store db.WalletStore,
	name string) uint32 {

	tb.Helper()

	walletInfo, err := store.CreateWallet(
		tb.Context(), CreateWalletParamsFixture(name),
	)
	require.NoError(tb, err)

	return walletInfo.ID
}

// createPaginationBenchmarkDerivedAccount creates one deterministic benchmark
// account.
func createPaginationBenchmarkDerivedAccount(tb testing.TB,
	store db.AccountStore, walletID uint32, scope db.KeyScope, name string) {

	tb.Helper()

	_, err := store.CreateDerivedAccount(
		tb.Context(), db.CreateDerivedAccountParams{
			WalletID: walletID,
			Scope:    scope,
			Name:     name,
		},
	)
	require.NoError(tb, err)
}

// createPaginationBenchmarkDerivedAddresses creates deterministic derived
// addresses using the same derive function pattern as address store tests.
func createPaginationBenchmarkDerivedAddresses(tb testing.TB,
	store db.AddressStore, walletID uint32, scope db.KeyScope,
	accountName string, change bool, count int) []db.AddressInfo {

	tb.Helper()

	addresses := make([]db.AddressInfo, 0, count)
	deriveFn := mockDeriveFunc()
	for i := 0; i < count; i++ {
		info, err := store.NewDerivedAddress(
			tb.Context(), db.NewDerivedAddressParams{
				WalletID:    walletID,
				Scope:       scope,
				AccountName: accountName,
				Change:      change,
			}, deriveFn,
		)
		require.NoError(tb, err)

		addresses = append(addresses, *info)
	}

	return addresses
}

// decimalWidth returns the number of base-10 digits needed to print n.
func decimalWidth(n int) int {
	width := 1
	for n >= 10 {
		n /= 10
		width++
	}

	return width
}

// maxInt returns the larger of a or b.
func maxInt(a int, b int) int {
	if a > b {
		return a
	}

	return b
}
