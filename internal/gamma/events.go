package gamma

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// ListEvents retrieves a list of events with optional filtering.
func (c *Client) ListEvents(ctx context.Context, params ListEventsParams) ([]Event, error) {
	q := buildEventParams(params)
	var events []Event
	if err := c.get(ctx, "/events", q, &events); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return events, nil
}

// GetEventByID retrieves a single event by its ID.
func (c *Client) GetEventByID(ctx context.Context, id int) (*Event, error) {
	var event Event
	if err := c.get(ctx, fmt.Sprintf("/events/%d", id), nil, &event); err != nil {
		return nil, fmt.Errorf("get event by id %d: %w", id, err)
	}
	return &event, nil
}

// GetEventBySlug retrieves a single event by its slug.
func (c *Client) GetEventBySlug(ctx context.Context, slug string) (*Event, error) {
	var event Event
	if err := c.get(ctx, "/events/slug/"+url.PathEscape(slug), nil, &event); err != nil {
		return nil, fmt.Errorf("get event by slug %q: %w", slug, err)
	}
	return &event, nil
}

func buildEventParams(p ListEventsParams) url.Values {
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
	if p.Closed != nil {
		q.Set("closed", strconv.FormatBool(*p.Closed))
	}
	if p.Active != nil {
		q.Set("active", strconv.FormatBool(*p.Active))
	}
	if p.TagID != nil {
		q.Set("tag_id", strconv.Itoa(*p.TagID))
	}
	if p.Slug != "" {
		q.Set("slug", p.Slug)
	}

	return q
}
