package gamma

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ListMarkets retrieves a list of markets with optional filtering.
func (c *Client) ListMarkets(ctx context.Context, params ListMarketsParams) ([]Market, error) {
	q := buildMarketParams(params)
	var markets []Market
	if err := c.get(ctx, "/markets", q, &markets); err != nil {
		return nil, fmt.Errorf("list markets: %w", err)
	}
	return markets, nil
}

// GetMarketByID retrieves a single market by its ID.
func (c *Client) GetMarketByID(ctx context.Context, id int) (*Market, error) {
	var market Market
	if err := c.get(ctx, fmt.Sprintf("/markets/%d", id), nil, &market); err != nil {
		return nil, fmt.Errorf("get market by id %d: %w", id, err)
	}
	return &market, nil
}

// GetMarketBySlug retrieves a single market by its slug.
func (c *Client) GetMarketBySlug(ctx context.Context, slug string) (*Market, error) {
	var market Market
	if err := c.get(ctx, "/markets/slug/"+url.PathEscape(slug), nil, &market); err != nil {
		return nil, fmt.Errorf("get market by slug %q: %w", slug, err)
	}
	return &market, nil
}

func buildMarketParams(p ListMarketsParams) url.Values {
	q := url.Values{}

	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Offset > 0 {
		q.Set("offset", strconv.Itoa(p.Offset))
	}
	if p.Order != "" {
		q.Set("order", p.Order)
	}
	if p.Ascending != nil {
		q.Set("ascending", strconv.FormatBool(*p.Ascending))
	}
	if len(p.ID) > 0 {
		ids := make([]string, len(p.ID))
		for i, id := range p.ID {
			ids[i] = strconv.Itoa(id)
		}
		q.Set("id", strings.Join(ids, ","))
	}
	if len(p.Slug) > 0 {
		q.Set("slug", strings.Join(p.Slug, ","))
	}
	if len(p.ClobTokenIDs) > 0 {
		q.Set("clob_token_ids", strings.Join(p.ClobTokenIDs, ","))
	}
	if len(p.ConditionIDs) > 0 {
		q.Set("condition_ids", strings.Join(p.ConditionIDs, ","))
	}
	if p.LiquidityNumMin != nil {
		q.Set("liquidity_num_min", strconv.FormatFloat(*p.LiquidityNumMin, 'f', -1, 64))
	}
	if p.LiquidityNumMax != nil {
		q.Set("liquidity_num_max", strconv.FormatFloat(*p.LiquidityNumMax, 'f', -1, 64))
	}
	if p.VolumeNumMin != nil {
		q.Set("volume_num_min", strconv.FormatFloat(*p.VolumeNumMin, 'f', -1, 64))
	}
	if p.VolumeNumMax != nil {
		q.Set("volume_num_max", strconv.FormatFloat(*p.VolumeNumMax, 'f', -1, 64))
	}
	if p.StartDateMin != nil {
		q.Set("start_date_min", p.StartDateMin.Format("2006-01-02T15:04:05Z"))
	}
	if p.StartDateMax != nil {
		q.Set("start_date_max", p.StartDateMax.Format("2006-01-02T15:04:05Z"))
	}
	if p.EndDateMin != nil {
		q.Set("end_date_min", p.EndDateMin.Format("2006-01-02T15:04:05Z"))
	}
	if p.EndDateMax != nil {
		q.Set("end_date_max", p.EndDateMax.Format("2006-01-02T15:04:05Z"))
	}
	if p.TagID != nil {
		q.Set("tag_id", strconv.Itoa(*p.TagID))
	}
	if p.RelatedTags != nil {
		q.Set("related_tags", strconv.FormatBool(*p.RelatedTags))
	}
	if p.Closed != nil {
		q.Set("closed", strconv.FormatBool(*p.Closed))
	}
	if p.Active != nil {
		q.Set("active", strconv.FormatBool(*p.Active))
	}

	return q
}
