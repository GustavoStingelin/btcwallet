package page

import (
	"context"
	"errors"
	"fmt"
)

const (
	// DefaultPageSize is the default number of items returned in a page.
	DefaultPageSize = 100

	// MaxPageSize is the maximum number of items that can be returned in a
	// page. SQL queries that implement early-exhaustion lookahead may receive
	// MaxPageSize+1 as the page limit.
	MaxPageSize = 1000
)

// errNilItem is a sentinel for an internal invariant violation: a toItem
// converter returned (nil, nil), which would panic on dereference and
// indicates a bug in the converter.
var errNilItem = errors.New("toItem returned nil with no error")

// Request holds the parameters for a paginated list query. The zero value is
// valid and requests the first page at DefaultPageSize. All With* methods
// return a modified copy (value-type immutability).
type Request[C any] struct {
	// size is the maximum number of items to return per page.
	size uint32

	// cursor is the pagination cursor from the previous page.
	// Nil means the first page.
	cursor *C

	// earlyExhaustion controls whether LastCursor and HasMore use deferred
	// (false, default) or early (true) semantics.
	earlyExhaustion bool
}

// Size returns the normalized requested page size for this request. A size of
// zero returns DefaultPageSize. A size greater than MaxPageSize is clamped to
// MaxPageSize.
func (r Request[C]) Size() uint32 {
	if r.size == 0 {
		return DefaultPageSize
	}

	if r.size > MaxPageSize {
		return MaxPageSize
	}

	return r.size
}

// QueryLimit returns the number of rows the SQL query should fetch. When early
// exhaustion is enabled, it returns Size+1 to allow HasMore detection without
// an extra round-trip. Otherwise, it returns Size.
func (r Request[C]) QueryLimit() uint32 {
	if r.earlyExhaustion {
		return r.Size() + 1
	}

	return r.Size()
}

// WithSize returns a copy of request with size replaced. The size is not
// validated here; normalization (zero -> DefaultPageSize, over MaxPageSize ->
// MaxPageSize) happens in Size() and QueryLimit(). A caller passing 0 or a
// large value will not see an error, the value will just be normalized later.
func (r Request[C]) WithSize(size uint32) Request[C] {
	r.size = size

	return r
}

// Cursor returns the pagination cursor from the previous page.
// A nil return value means the first page is being requested.
func (r Request[C]) Cursor() *C {
	return r.cursor
}

// WithCursor returns a copy of the request with the cursor replaced. Calling
// this on a zero-value Request produces a request for the second page (the page
// after this cursor). It takes the cursor by value to avoid the caller
// retaining a pointer into the Request.
func (r Request[C]) WithCursor(cursor C) Request[C] {
	r.cursor = &cursor

	return r
}

// EarlyExhaustion reports whether the request uses early-exhaustion
// mode. See WithEarlyExhaustion for a full description.
func (r Request[C]) EarlyExhaustion() bool {
	return r.earlyExhaustion
}

// WithEarlyExhaustion enables early exhaustion detection for the request.
//
// Default behavior (disabled):
//   - Provides a stronger guarantee: exhaustion is only concluded after an
//     empty result, so it reflects a stable and complete view of the dataset
//     at the time of the final fetch.
//   - The query fetches exactly Size rows.
//   - HasMore is true whenever the page is non-empty.
//   - End of iteration is only known after an extra fetch that returns no rows.
//   - Zero extra work per query.
//   - Requires one additional round-trip at the end.
//
// Early exhaustion enabled:
//   - HasMore = false reflects the state at query time only. Rows inserted
//     after the query may not be observed, so exhaustion is not a strict
//     completeness guarantee under concurrent writes.
//   - The query fetches Size+1 rows internally and returns at most Size rows.
//   - If an extra row is present, HasMore = true.
//   - If not, HasMore = false, meaning this is the last page.
//   - Avoids the final round-trip.
//   - Adds a very small overhead per query due to fetching one extra row.
func (r Request[C]) WithEarlyExhaustion() Request[C] {
	r.earlyExhaustion = true

	return r
}

// BuildResult assembles a page.Result from a slice of items already fetched by
// the caller. It uses r.Size and r.EarlyExhaustion to determine HasMore and
// LastCursor. The toCursor function is called on the last item of the possibly
// trimmed slice.
//
// An empty slice always returns an empty result regardless of mode.
//
// When early exhaustion is disabled (default):
//   - Any non-empty slice sets HasMore to true and LastCursor to the
//     cursor of the last item.
//
// When early exhaustion is enabled:
//   - If len(items) is greater than Size, it trims to Size and sets
//     HasMore to true.
//   - If len(items) is non-empty and less than or equal to Size,
//     HasMore is false.
func BuildResult[Cursor, Item any](r Request[Cursor], items []Item,
	toCursor func(Item) Cursor) Result[Item, Cursor] {

	if len(items) == 0 {
		return Result[Item, Cursor]{Items: items}
	}

	if !r.EarlyExhaustion() {
		last := items[len(items)-1]
		cursor := toCursor(last)

		return Result[Item, Cursor]{
			Items:      items,
			LastCursor: &cursor,
			HasMore:    true,
		}
	}

	pageSize := r.Size()

	hasMore := len(items) > int(pageSize)
	if hasMore {
		items = items[:int(pageSize)]
	}

	last := items[len(items)-1]
	cursor := toCursor(last)

	return Result[Item, Cursor]{
		Items:      items,
		LastCursor: &cursor,
		HasMore:    hasMore,
	}
}

// mapRows converts rows into items using toItem. It allocates a new slice of
// exactly len(rows) and returns immediately if row conversion fails. It
// guards against a (nil, nil) return from toItem, which indicates a bug in
// the converter.
func mapRows[Row, Item any](rows []Row,
	toItem func(Row) (*Item, error)) ([]Item, error) {

	items := make([]Item, len(rows))
	for i, row := range rows {
		item, err := toItem(row)
		if err != nil {
			return nil, err
		}

		if item == nil {
			return nil, errNilItem
		}

		items[i] = *item
	}

	return items, nil
}

// FetchFirstOrNext fetches a paginated item list using either firstPage or
// nextPage based on whether req has a cursor. The firstPage and firstArgs are
// called when req.Cursor() is nil (first page). The nextPage and nextArgs are
// called otherwise, with the nextArgs receiving the cursor value. The entity
// parameter is used only in error messages to identify the resource type
// (e.g., "account", "transaction"). The firstToItem and nextToItem functions
// are row-to-domain converters; they may differ because sqlc generates
// distinct row types per backend. FetchFirstOrNext returns a flat []Item;
// Result assembly via BuildResult is left to the caller.
func FetchFirstOrNext[FirstRow, FirstArgs, NextRow, NextArgs, Item,
	Cursor any](ctx context.Context,
	firstPage func(context.Context, FirstArgs) ([]FirstRow, error),
	firstArgs func(uint32) FirstArgs,
	nextPage func(context.Context, NextArgs) ([]NextRow, error),
	nextArgs func(Cursor, uint32) NextArgs,
	req Request[Cursor], entity string,
	firstToItem func(FirstRow) (*Item, error),
	nextToItem func(NextRow) (*Item, error)) ([]Item, error) {

	if req.Cursor() == nil {
		rows, err := firstPage(ctx, firstArgs(req.QueryLimit()))
		if err != nil {
			return nil, fmt.Errorf("list %s first page: %w", entity, err)
		}

		items, err := mapRows(rows, firstToItem)
		if err != nil {
			return nil, fmt.Errorf("map %s first page rows: %w", entity, err)
		}

		return items, nil
	}

	rows, err := nextPage(ctx, nextArgs(*req.Cursor(), req.QueryLimit()))
	if err != nil {
		return nil, fmt.Errorf("list %s next page: %w", entity, err)
	}

	items, err := mapRows(rows, nextToItem)
	if err != nil {
		return nil, fmt.Errorf("map %s next page rows: %w", entity, err)
	}

	return items, nil
}
