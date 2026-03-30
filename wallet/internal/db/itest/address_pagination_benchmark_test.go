//go:build itest

package itest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type addressPaginationBenchmarkCase struct {
	Name string

	Query paginationBenchmarkSQLQuery

	Plan paginationBenchmarkQueryPlan

	ExpectedItems int
}

// BenchmarkAddressPagination benchmarks first-page and next-page address
// pagination queries across the shared dataset sizes.
func BenchmarkAddressPagination(b *testing.B) {
	for _, datasetSize := range paginationBenchmarkDatasetSizes {
		datasetName := paginationBenchmarkDatasetName(
			datasetSize, "Addresses",
		)

		b.Run(datasetName, func(b *testing.B) {
			for _, pageSize := range paginationBenchmarkPageSizes {
				pageSizeName := paginationBenchmarkPageSizeName(pageSize)

				b.Run(pageSizeName, func(b *testing.B) {
					store := NewTestStore(b)
					benchmarkCases :=
						prepareAddressPaginationBenchmarkCasesForPageSize(
							b, store, datasetSize, pageSize,
						)

					for _, benchmarkCase := range benchmarkCases {
						b.Run(benchmarkCase.Name, func(b *testing.B) {
							runAddressPaginationBenchmarkCase(
								b, store, benchmarkCase,
							)
						})
					}
				})
			}
		})
	}
}

func prepareAddressPaginationBenchmarkCases(tb testing.TB,
	store paginationBenchmarkStore,
	datasetSize int) []addressPaginationBenchmarkCase {

	return prepareAddressPaginationBenchmarkCasesForPageSize(
		tb, store, datasetSize, paginationBenchmarkPageSizes[0],
	)
}

func prepareAddressPaginationBenchmarkCasesForPageSize(tb testing.TB,
	store paginationBenchmarkStore, datasetSize int,
	pageSize uint32) []addressPaginationBenchmarkCase {

	tb.Helper()

	if datasetSize <= 0 {
		tb.Fatalf("address benchmark dataset too small: %d", datasetSize)
	}

	seed := seedPaginationBenchmarkAddresses(tb, store,
		paginationBenchmarkAddressSeedConfig{Count: datasetSize},
	)
	require.Len(tb, seed.Addresses, datasetSize)
	backend := mustPaginationBenchmarkBackend(tb, store)
	expectedFirstPageItems := minInt(datasetSize, int(pageSize))

	firstPageQuery := paginationBenchmarkAddressFirstPageQuery(
		seed.WalletID, seed.Scope, seed.AccountName,
		pageSize, false,
	)
	splitFirstPageQuery := addressPaginationBenchmarkQuery(
		backend, paginationBenchmarkQueryVariantSplit, firstPageQuery,
	)
	splitFirstPageResult := runPaginationBenchmarkQuery(
		tb, store.DB(), splitFirstPageQuery,
	)
	require.Equal(tb, expectedFirstPageItems, splitFirstPageResult.RowCount)
	require.NotNil(tb, splitFirstPageResult.LastCursor)

	unifiedFirstPageQuery := addressPaginationBenchmarkQuery(
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

	firstPagePlan := captureAddressPaginationVariantQueryPlan(
		tb, store, paginationBenchmarkQueryVariantSplit, firstPageQuery,
	)
	require.NotEmpty(tb, firstPagePlan.Lines)

	firstPageUnifiedPlan := captureAddressPaginationVariantQueryPlan(
		tb, store, paginationBenchmarkQueryVariantUnified, firstPageQuery,
	)
	require.NotEmpty(tb, firstPageUnifiedPlan.Lines)

	benchmarkCases := []addressPaginationBenchmarkCase{
		{
			Name: paginationBenchmarkScenarioCaseName(
				true, paginationBenchmarkQueryVariantSplit,
			),
			Query:         splitFirstPageQuery,
			Plan:          firstPagePlan,
			ExpectedItems: splitFirstPageResult.RowCount,
		},
		{
			Name: paginationBenchmarkScenarioCaseName(
				true, paginationBenchmarkQueryVariantUnified,
			),
			Query:         unifiedFirstPageQuery,
			Plan:          firstPageUnifiedPlan,
			ExpectedItems: unifiedFirstPageResult.RowCount,
		},
	}

	if !paginationBenchmarkHasNextPage(datasetSize, pageSize) {
		return benchmarkCases
	}

	expectedNextPageItems := minInt(
		datasetSize-int(pageSize), int(pageSize),
	)
	nextPageQuery := paginationBenchmarkAddressNextPageQuery(
		seed.WalletID, seed.Scope, seed.AccountName,
		pageSize, *splitFirstPageResult.LastCursor, false,
	)
	splitNextPageQuery := addressPaginationBenchmarkQuery(
		backend, paginationBenchmarkQueryVariantSplit, nextPageQuery,
	)
	splitNextPageResult := runPaginationBenchmarkQuery(
		tb, store.DB(), splitNextPageQuery,
	)
	require.Equal(tb, expectedNextPageItems, splitNextPageResult.RowCount)

	unifiedNextPageQuery := addressPaginationBenchmarkQuery(
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

	nextPagePlan := captureAddressPaginationVariantQueryPlan(
		tb, store, paginationBenchmarkQueryVariantSplit, nextPageQuery,
	)
	require.NotEmpty(tb, nextPagePlan.Lines)

	nextPageUnifiedPlan := captureAddressPaginationVariantQueryPlan(
		tb, store, paginationBenchmarkQueryVariantUnified, nextPageQuery,
	)
	require.NotEmpty(tb, nextPageUnifiedPlan.Lines)

	return append(benchmarkCases,
		addressPaginationBenchmarkCase{
			Name: paginationBenchmarkScenarioCaseName(
				false, paginationBenchmarkQueryVariantSplit,
			),
			Query:         splitNextPageQuery,
			Plan:          nextPagePlan,
			ExpectedItems: splitNextPageResult.RowCount,
		},
		addressPaginationBenchmarkCase{
			Name: paginationBenchmarkScenarioCaseName(
				false, paginationBenchmarkQueryVariantUnified,
			),
			Query:         unifiedNextPageQuery,
			Plan:          nextPageUnifiedPlan,
			ExpectedItems: unifiedNextPageResult.RowCount,
		},
	)
}

func runAddressPaginationBenchmarkCase(b *testing.B,
	store paginationBenchmarkStore, benchmarkCase addressPaginationBenchmarkCase) {

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
