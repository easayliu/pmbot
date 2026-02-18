package gamma

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const defaultSearchPageSize = 100

// ErrKeywordRequired is returned when a search operation is called without a keyword.
var ErrKeywordRequired = errors.New("keyword is required")

// SearchEventsParams contains parameters for keyword-based event search.
type SearchEventsParams struct {
	// Keyword is the search term matched against event title and description.
	Keyword string
	// Closed filters by market open/closed status.
	Closed *bool
	// MaxPages limits the number of API pages to scan. Defaults to 5.
	MaxPages int
}

// SearchEvents fetches events in batches and filters by keyword client-side.
// The Gamma API does not support server-side text search, so this performs
// paginated fetching with local keyword matching.
func (c *Client) SearchEvents(ctx context.Context, params SearchEventsParams) ([]Event, error) {
	if params.Keyword == "" {
		return nil, ErrKeywordRequired
	}
	maxPages := params.MaxPages
	if maxPages <= 0 {
		maxPages = 5
	}
	keyword := strings.ToLower(params.Keyword)

	var matched []Event
	for page := 0; page < maxPages; page++ {
		events, err := c.ListEvents(ctx, ListEventsParams{
			Limit:  defaultSearchPageSize,
			Offset: page * defaultSearchPageSize,
			Closed: params.Closed,
		})
		if err != nil {
			return nil, fmt.Errorf("search events page %d: %w", page, err)
		}
		for _, e := range events {
			if containsKeyword(e.Title, keyword) || containsKeyword(e.Description, keyword) {
				matched = append(matched, e)
			}
		}
		// Stop early if we got fewer results than the page size.
		if len(events) < defaultSearchPageSize {
			break
		}
	}
	return matched, nil
}

// SearchMarkets fetches markets in batches and filters by keyword client-side.
func (c *Client) SearchMarkets(ctx context.Context, keyword string, closed *bool) ([]Market, error) {
	if keyword == "" {
		return nil, ErrKeywordRequired
	}
	kw := strings.ToLower(keyword)

	var matched []Market
	for page := 0; page < 5; page++ {
		markets, err := c.ListMarkets(ctx, ListMarketsParams{
			Limit:  defaultSearchPageSize,
			Offset: page * defaultSearchPageSize,
			Closed: closed,
		})
		if err != nil {
			return nil, fmt.Errorf("search markets page %d: %w", page, err)
		}
		for _, m := range markets {
			if containsKeyword(m.Question, kw) || containsKeyword(m.Description, kw) {
				matched = append(matched, m)
			}
		}
		if len(markets) < defaultSearchPageSize {
			break
		}
	}
	return matched, nil
}

func containsKeyword(text, keyword string) bool {
	return strings.Contains(strings.ToLower(text), keyword)
}
