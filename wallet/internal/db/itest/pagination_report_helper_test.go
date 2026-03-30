//go:build itest

package itest

import (
	"fmt"
	"testing"
)

// TestPrintQueryPlans prints the query plans captured by the pagination
// benchmarks. This is a helper for report generation.
//
// Run with:
// go test -v -tags=itest ./wallet/internal/db/itest -run TestPrintQueryPlans
// go test -v -tags='itest test_db_postgres' ./wallet/internal/db/itest -run TestPrintQueryPlans
func TestPrintQueryPlans(t *testing.T) {
	store := NewTestStore(t)
	datasetSize := 10 // Use small dataset for plan capture

	t.Run("Wallet", func(t *testing.T) {
		cases := prepareWalletPaginationBenchmarkCases(t, store, datasetSize)
		for _, tc := range cases {
			fmt.Printf("--- BACKEND: %s ---\n", tc.Plan.Backend)
			fmt.Printf("--- SCENARIO: Wallet %s ---\n", tc.Name)
			fmt.Printf("--- QUERY ---\n%s\n", tc.Plan.Query)
			fmt.Printf("--- PLAN ---\n%s\n\n", tc.Plan.Text())
		}
	})

	t.Run("Address", func(t *testing.T) {
		cases := prepareAddressPaginationBenchmarkCases(t, store, datasetSize)
		for _, tc := range cases {
			fmt.Printf("--- BACKEND: %s ---\n", tc.Plan.Backend)
			fmt.Printf("--- SCENARIO: Address %s ---\n", tc.Name)
			fmt.Printf("--- QUERY ---\n%s\n", tc.Plan.Query)
			fmt.Printf("--- PLAN ---\n%s\n\n", tc.Plan.Text())
		}
	})
}
