# Pagination Performance Comparison: Split vs Unified (Experimental)

This report documents a benchmark-only performance comparison between the
traditional **split-query** approach used in production (separate SQL for
first-page and next-page) and an **experimental unified-query** approach
using an optional cursor with `NULL` checks.

The results below reflect the current state of the codebase, including
relevant indexes for both address and wallet tables.

## Reproduction Commands

Use these commands to reproduce the benchmark measurements and query
plans. PostgreSQL benchmarks require a running Docker daemon.

### 1. Timing Benchmarks (Wall Clock)
To reproduce the timing results for the original **page size 5** baseline,
use `-count=10`:
```bash
# SQLite
go test -v -tags=itest ./wallet/internal/db/itest -run '^$' -bench BenchmarkAddressPagination -count=10
go test -v -tags=itest ./wallet/internal/db/itest -run '^$' -bench BenchmarkWalletPagination -count=10

# PostgreSQL
go test -v -tags='itest test_db_postgres' ./wallet/internal/db/itest -run '^$' -bench BenchmarkAddressPagination -count=10
go test -v -tags='itest test_db_postgres' ./wallet/internal/db/itest -run '^$' -bench BenchmarkWalletPagination -count=10
```

To reproduce the **page size 100** and **page size 500** findings, use
`-count=5` for faster representative sampling:
```bash
# SQLite
go test -v -tags=itest ./wallet/internal/db/itest -run '^$' -bench BenchmarkAddressPagination -count=5
go test -v -tags=itest ./wallet/internal/db/itest -run '^$' -bench BenchmarkWalletPagination -count=5

# PostgreSQL
go test -v -tags='itest test_db_postgres' ./wallet/internal/db/itest -run '^$' -bench BenchmarkAddressPagination -count=5
go test -v -tags='itest test_db_postgres' ./wallet/internal/db/itest -run '^$' -bench BenchmarkWalletPagination -count=5
```

### 2. Query Plan Capture (EXPLAIN)
To capture and inspect the exact query plans for both approaches, run the
following helper tests:
```bash
# SQLite
go test -v -tags=itest ./wallet/internal/db/itest -run TestPrintQueryPlans

# PostgreSQL
go test -v -tags='itest test_db_postgres' ./wallet/internal/db/itest -run TestPrintQueryPlans
```

---

## Measured Benchmark Evidence (Wallet Pagination)

Benchmarks were run against four dataset sizes (10, 100, 1,000, 10,000) using
page sizes of 5, 100, and 500. Timing results represent the current state.
Values for page size 5 use `-count=10`; page sizes 100 and 500 use `-count=5`.

Next-page scenarios are only benchmarked when the dataset size exceeds the
page size. Invalid combinations (e.g., page size 100 with dataset 10) are
skipped to ensure only valid cursor-based transitions are measured.

### Wallet Pagination

Wallets are ordered by `id` (primary key).

#### SQLite Wallet Timing

| Scenario | Split | Unified | Diff |
| :--- | :--- | :--- | :--- |
| **Page Size 5** | | | |
| 10 First | 39,427-42,558 | 45,947-47,027 | ~+13% |
| 10 Next | 44,695-46,015 | 45,647-46,475 | ~+2% |
| 100 First | 39,635-42,554 | 42,714-45,548 | ~+8% |
| 100 Next | 43,868-44,359 | 45,224-45,445 | ~+3% |
| 1,000 First | 40,366-42,431 | 43,549-44,404 | ~+6% |
| 1,000 Next | 41,852-42,509 | 42,695-43,548 | ~+2% |
| 10,000 First | 39,596-42,150 | 42,847-45,347 | ~+8% |
| 10,000 Next | 41,548-42,135 | 42,830-43,153 | ~+3% |
| **Page Size 100** | | | |
| 10 First | 50,824-52,299 | 54,609-55,021 | ~+7% |
| 100 First | 162,399-191,189 | 165,430-170,731 | ~+2% |
| 1,000 First | 162,538-165,725 | 163,566-165,538 | ~Neutral |
| 1,000 Next | 159,749-162,487 | 166,976-168,382 | ~+5% |
| 10,000 First | 163,335-168,182 | 169,432-193,409 | ~+10% |
| 10,000 Next | 167,328-171,538 | 173,345-175,967 | ~+3% |
| **Page Size 500** | | | |
| 10 First | 50,304-51,828 | 53,579-54,514 | ~+6% |
| 100 First | 162,527-168,929 | 165,910-167,161 | ~+1% |
| 1,000 First | 635,633-650,702 | 656,815-667,512 | ~+3% |
| 1,000 Next | 650,808-670,074 | 675,858-684,495 | ~+3% |
| 10,000 First | 650,266-656,328 | 648,214-657,669 | ~Neutral |
| 10,000 Next | 652,394-663,611 | 673,862-696,551 | ~+4% |

#### PostgreSQL Wallet Timing

| Scenario | Split | Unified | Diff |
| :--- | :--- | :--- | :--- |
| **Page Size 5** | | | |
| 10 First | 240,714-275,840 | 154,441-163,669 | ~-38% |
| 10 Next | 157,821-163,835 | 154,411-158,889 | ~-3% |
| 100 First | 247,584-258,588 | 149,477-154,802 | ~-40% |
| 100 Next | 148,894-152,645 | 148,787-152,206 | ~-0% |
| 1,000 First | 217,820-228,147 | 220,192-227,229 | ~Neutral |
| 1,000 Next | 226,490-229,370 | 268,242-305,174 | ~+25% |
| 10,000 First | 244,457-289,713 | 292,240-300,303 | ~+11% |
| 10,000 Next | 288,013-306,285 | 292,868-300,395 | ~Neutral |
| **Page Size 100** | | | |
| 10 First | 190,622-195,596 | 191,603-206,493 | Mixed |
| 100 First | 339,586-366,706 | 347,415-396,591 | Mixed |
| 1,000 First | 536,783-568,743 | 424,358-449,388 | ~-20% |
| 1,000 Next | 391,068-449,399 | 268,909-452,098 | Mixed |
| 10,000 First | 397,389-432,680 | 398,127-444,831 | ~Neutral |
| 10,000 Next | 395,710-427,210 | 423,254-432,181 | ~+4% |
| **Page Size 500** | | | |
| 10 First | 192,070-201,962 | 181,957-231,287 | Mixed |
| 100 First | 341,783-373,018 | 344,498-352,894 | ~Neutral |
| 1,000 First | 678,641-763,257 | 670,312-708,945 | ~Neutral |
| 1,000 Next | 711,380-775,273 | 696,462-807,491 | Mixed |
| 10,000 First | 923,930-986,288 | 697,173-737,582 | ~-25% |
| 10,000 Next | 725,987-807,857 | 508,218-800,751 | ~Neutral |

**Analysis**: SQLite consistently favors the split approach for wallets, as it
enables direct use of the integer primary key for next-page lookups. This
preference remains stable as page sizes increase to 100 and 500. In PostgreSQL,
the unified approach shows strong advantages in specific scenarios (e.g., small
datasets with page size 5, or large datasets with page size 500), but the
results are more sensitive to page size and dataset volume, making it difficult
to declare a single global winner.

---

## Measured Benchmark Evidence (Address Pagination)

Benchmarks were run against four dataset sizes (10, 100, 1,000, 10,000) using
page sizes of 5, 100, and 500. Timing results reflect the current indexed state.
Values for page size 5 use `-count=10`; page sizes 100 and 500 use `-count=5`.

### Address Pagination (Indexed)

Addresses are filtered by `account_id` and ordered by `id`. The current schema
utilizes `idx_addresses_by_account_pagination (account_id, id)`.

#### SQLite Address Timing

| Scenario | Split | Unified | Diff |
| :--- | :--- | :--- | :--- |
| **Page Size 5** | | | |
| 10 First | 53,474-56,623 | 59,207-60,042 | ~+8% |
| 10 Next | 58,072-58,757 | 58,863-59,518 | ~+1% |
| 100 First | 54,100-54,692 | 57,729-58,139 | ~+6% |
| 100 Next | 56,923-57,249 | 58,369-58,690 | ~+3% |
| 1,000 First | 50,868-54,240 | 54,455-54,812 | ~+4% |
| 1,000 Next | 53,745-54,133 | 54,986-55,343 | ~+2% |
| 10,000 First | 51,072-54,765 | 58,657-59,136 | ~+11% |
| 10,000 Next | 58,111-58,396 | 59,498-59,800 | ~+2% |
| **Page Size 100** | | | |
| 10 First | 65,045-66,182 | 68,880-69,689 | ~+5% |
| 100 First | 185,325-220,937 | 189,284-198,314 | ~Neutral |
| 1,000 First | 185,141-192,316 | 187,156-190,335 | ~Neutral |
| 1,000 Next | 182,965-187,015 | 197,501-199,526 | ~+7% |
| 10,000 First | 184,096-189,019 | 191,870-219,393 | ~+10% |
| 10,000 Next | 190,407-192,889 | 203,051-204,449 | ~+6% |
| **Page Size 500** | | | |
| 10 First | 64,141-66,031 | 67,702-69,087 | ~+5% |
| 100 First | 185,509-189,027 | 190,049-190,444 | ~+1% |
| 1,000 First | 696,392-713,166 | 719,132-736,468 | ~+3% |
| 1,000 Next | 708,796-731,061 | 771,295-782,027 | ~+8% |
| 10,000 First | 699,131-725,140 | 699,714-782,926 | ~+4% |
| 10,000 Next | 695,615-824,307 | 758,084-852,618 | ~+6% |

#### PostgreSQL Address Timing

| Scenario | Split | Unified | Diff |
| :--- | :--- | :--- | :--- |
| **Page Size 5** | | | |
| 10 First | 143,877-169,097 | 160,002-168,366 | Mixed |
| 10 Next | 160,391-164,260 | 158,532-160,671 | Mixed |
| 100 First | 165,906-170,680 | 162,583-166,799 | Mixed |
| 100 Next | 159,130-162,220 | 160,477-162,240 | Mixed |
| 1,000 First | 444,324-478,444 | 478,692-487,590 | ~+5% |
| 1,000 Next | 477,885-487,376 | 463,925-491,691 | Mixed |
| 10,000 First | 129,112-132,645 | 126,143-128,709 | ~-3% |
| 10,000 Next | 120,479-130,115 | 114,730-118,446 | ~-7% |
| **Page Size 100** | | | |
| 10 First | 200,061-205,982 | 196,616-204,508 | ~Neutral |
| 100 First | 337,353-366,896 | 348,062-412,055 | Mixed |
| 1,000 First | 725,559-772,989 | 674,048-720,590 | ~-7% |
| 1,000 Next | 638,455-669,892 | 648,216-702,153 | Mixed |
| 10,000 First | 3,382,552-3,739,418 | 3,506,342-3,661,476 | ~Neutral |
| 10,000 Next | 3,242,539-3,332,802 | 3,441,174-3,550,956 | ~+6% |
| **Page Size 500** | | | |
| 10 First | 200,937-212,337 | 206,253-260,684 | Mixed |
| 100 First | 346,941-365,251 | 344,760-354,391 | ~Neutral |
| 1,000 First | 954,447-1,084,234 | 1,014,417-1,118,207 | ~+5% |
| 1,000 Next | 775,850-826,235 | 837,755-864,291 | ~+6% |
| 10,000 First | 664,429-749,384 | 741,109-782,926 | ~+8% |
| 10,000 Next | 772,766-824,307 | 797,702-852,618 | ~+4% |

**Analysis**: In SQLite, the split approach remains consistently faster than the
unified approach for addresses across all page sizes. While the margin is often
small, the overhead of the unified `OR` clause becomes slightly more
pronounced as page sizes and next-page lookups scale. In PostgreSQL, address
results are highly varied; the unified approach performs well on small pages
and large datasets but can introduce regressions as page sizes grow (e.g.,
page size 500).

---

## Query Plan Observations (Representative EXPLAIN)

Larger page sizes (100, 500) do not change the fundamental query plans
captured for split vs unified queries; they only modify the `LIMIT` value.

### 1. SQLite Observations

SQLite shows a clear preference for the split approach when it can use a range
predicate on a primary key or index.

#### Wallet Plans
- **First Page (Split/Unified)**: Both use `SCAN w`.
- **Next Page (Split)**: `SEARCH w USING INTEGER PRIMARY KEY (rowid>?)`.
- **Next Page (Unified)**: `SCAN w`. The `OR` predicate disables the index range.

#### Address Plans
- **Next Page (Split)**: `SEARCH a USING INDEX idx_addresses_by_account_pagination (account_id=? AND id>?)`.
- **Next Page (Unified)**: `SEARCH a USING INDEX idx_addresses_by_account_pagination (account_id=?)`.

### 2. PostgreSQL Observations

PostgreSQL successfully applies index scans for both query variants across both
tables.

#### Wallet Plans
- **Next Page (Split/Unified)**: `Index Scan using wallets_pkey (id > '5'::bigint)`. Execution times are extremely low (~0.025-0.030 ms).

#### Address Plans
- **Next Page (Split/Unified)**: `Index Scan using idx_addresses_by_account_pagination`. Address plans currently include a `Sort` node over the scan in the captured output.

---

## Conclusion & Recommendation

Across all measured page sizes (5, 100, 500) and dataset volumes, the unified
approach demonstrates a modest performance overhead in SQLite, typically
ranging from 2% to 11%. While the split-query approach remains the
theoretical performance leader for SQLite by enabling direct range
predicates, the unified approach often shows neutral or even superior
performance in PostgreSQL.

Based on these findings, we recommend adopting the **unified-query** approach
for all pagination logic. The measured overhead is an acceptable tradeoff for
the significant maintainability gains of removing split-query complexity.
Consolidating into a single query shape simplifies the codebase, reduces the
surface area for bugs, and eases the burden of maintaining two distinct SQL
patterns for every paginated endpoint.

**Caveats:**
1. **Benchmark-Scoped**: This recommendation is grounded in current benchmark
   evidence and intended for the internal SQL store transition.
2. **Planner Sensitivity**: Results for optional cursor predicates can vary
   based on database version and distribution statistics.
3. **Local Environment**: Findings reflect performance on local benchmark
   hardware; production performance may vary under concurrent load.
