//go:build itest

package itest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type walletPaginationBenchmarkCase struct {
	Name string

	Query paginationBenchmarkSQLQuery

	Plan paginationBenchmarkQueryPlan

	ExpectedItems int
}

func BenchmarkWalletPagination(b *testing.B) {
	for _, datasetSize := range paginationBenchmarkDatasetSizes {
		datasetName := paginationBenchmarkDatasetName(datasetSize, "Wallets")

		b.Run(datasetName, func(b *testing.B) {
			for _, pageSize := range paginationBenchmarkPageSizes {
				pageSizeName := paginationBenchmarkPageSizeName(pageSize)

				b.Run(pageSizeName, func(b *testing.B) {
					store := NewTestStore(b)
					benchmarkCases :=
						prepareWalletPaginationBenchmarkCasesForPageSize(
							b, store, datasetSize, pageSize,
						)

					for _, benchmarkCase := range benchmarkCases {
						b.Run(benchmarkCase.Name, func(b *testing.B) {
							runWalletPaginationBenchmarkCase(
								b, store, benchmarkCase,
							)
						})
					}
				})
			}
		})
	}
}

func prepareWalletPaginationBenchmarkCases(tb testing.TB,
	store paginationBenchmarkStore,
	datasetSize int) []walletPaginationBenchmarkCase {

	return prepareWalletPaginationBenchmarkCasesForPageSize(
		tb, store, datasetSize, paginationBenchmarkPageSizes[0],
	)
}

func prepareWalletPaginationBenchmarkCasesForPageSize(tb testing.TB,
	store paginationBenchmarkStore, datasetSize int,
	pageSize uint32) []walletPaginationBenchmarkCase {

	tb.Helper()

	if datasetSize <= 0 {
		tb.Fatalf("wallet benchmark dataset too small: %d", datasetSize)
	}

	seedPaginationBenchmarkWallets(tb, store,
		paginationBenchmarkWalletSeedConfig{Count: datasetSize},
	)

	backend := mustPaginationBenchmarkBackend(tb, store)
	expectedFirstPageItems := minInt(datasetSize, int(pageSize))

	firstPageQuery := paginationBenchmarkWalletFirstPageQuery(
		pageSize, false,
	)
	splitFirstPageQuery := walletPaginationBenchmarkQuery(
		backend, paginationBenchmarkQueryVariantSplit, firstPageQuery,
	)
	splitFirstPageResult := runPaginationBenchmarkQuery(
		tb, store.DB(), splitFirstPageQuery,
	)
	require.Equal(tb, expectedFirstPageItems, splitFirstPageResult.RowCount)
	require.NotNil(tb, splitFirstPageResult.LastCursor)

	unifiedFirstPageQuery := walletPaginationBenchmarkQuery(
		backend, paginationBenchmarkQueryVariantUnified, firstPageQuery,
	)
	unifiedFirstPageResult := runPaginationBenchmarkQuery(
		tb, store.DB(), unifiedFirstPageQuery,
	)
	require.Equal(tb, splitFirstPageResult.RowCount,
		unifiedFirstPageResult.RowCount,
	)
	require.Equal(tb, splitFirstPageResult.LastCursor,
		unifiedFirstPageResult.LastCursor,
	)

	benchmarkCases := []walletPaginationBenchmarkCase{
		{
			Name: paginationBenchmarkScenarioCaseName(
				true, paginationBenchmarkQueryVariantSplit,
			),
			Query: splitFirstPageQuery,
			Plan: captureWalletPaginationVariantQueryPlan(
				tb, store, paginationBenchmarkQueryVariantSplit,
				firstPageQuery,
			),
			ExpectedItems: splitFirstPageResult.RowCount,
		},
		{
			Name: paginationBenchmarkScenarioCaseName(
				true, paginationBenchmarkQueryVariantUnified,
			),
			Query: unifiedFirstPageQuery,
			Plan: captureWalletPaginationVariantQueryPlan(
				tb, store, paginationBenchmarkQueryVariantUnified,
				firstPageQuery,
			),
			ExpectedItems: unifiedFirstPageResult.RowCount,
		},
	}

	if !paginationBenchmarkHasNextPage(datasetSize, pageSize) {
		return benchmarkCases
	}

	expectedNextPageItems := minInt(
		datasetSize-int(pageSize), int(pageSize),
	)
	nextPageQuery := paginationBenchmarkWalletNextPageQuery(
		pageSize, *splitFirstPageResult.LastCursor, false,
	)
	splitNextPageQuery := walletPaginationBenchmarkQuery(
		backend, paginationBenchmarkQueryVariantSplit, nextPageQuery,
	)
	splitNextPageResult := runPaginationBenchmarkQuery(
		tb, store.DB(), splitNextPageQuery,
	)
	require.Equal(tb, expectedNextPageItems, splitNextPageResult.RowCount)

	unifiedNextPageQuery := walletPaginationBenchmarkQuery(
		backend, paginationBenchmarkQueryVariantUnified, nextPageQuery,
	)
	unifiedNextPageResult := runPaginationBenchmarkQuery(
		tb, store.DB(), unifiedNextPageQuery,
	)
	require.Equal(tb, splitNextPageResult.RowCount,
		unifiedNextPageResult.RowCount,
	)
	require.Equal(tb, splitNextPageResult.LastCursor,
		unifiedNextPageResult.LastCursor,
	)

	return append(benchmarkCases,
		walletPaginationBenchmarkCase{
			Name: paginationBenchmarkScenarioCaseName(
				false, paginationBenchmarkQueryVariantSplit,
			),
			Query: splitNextPageQuery,
			Plan: captureWalletPaginationVariantQueryPlan(
				tb, store, paginationBenchmarkQueryVariantSplit,
				nextPageQuery,
			),
			ExpectedItems: splitNextPageResult.RowCount,
		},
		walletPaginationBenchmarkCase{
			Name: paginationBenchmarkScenarioCaseName(
				false, paginationBenchmarkQueryVariantUnified,
			),
			Query: unifiedNextPageQuery,
			Plan: captureWalletPaginationVariantQueryPlan(
				tb, store, paginationBenchmarkQueryVariantUnified,
				nextPageQuery,
			),
			ExpectedItems: unifiedNextPageResult.RowCount,
		},
	)
}

func runWalletPaginationBenchmarkCase(b *testing.B,
	store paginationBenchmarkStore, benchmarkCase walletPaginationBenchmarkCase) {

	b.Helper()

	require.NotEmpty(b, benchmarkCase.Plan.Lines)

	result := runPaginationBenchmarkQuery(
		b, store.DB(), benchmarkCase.Query,
	)
	require.Equal(b, benchmarkCase.ExpectedItems, result.RowCount)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		result = runPaginationBenchmarkQuery(
			b, store.DB(), benchmarkCase.Query,
		)
		require.Equal(b, benchmarkCase.ExpectedItems, result.RowCount)
	}
}
