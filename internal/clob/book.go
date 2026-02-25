package clob

import (
	"context"
	"fmt"
	"net/url"
)

// GetOrderBook retrieves the order book for a single token.
func (c *Client) GetOrderBook(ctx context.Context, tokenID string) (*OrderBook, error) {
	params := url.Values{"token_id": {tokenID}}
	var book OrderBook
	if err := c.get(ctx, "/book", params, &book); err != nil {
		return nil, fmt.Errorf("get order book: %w", err)
	}
	return &book, nil
}

// GetOrderBooks retrieves order books for multiple tokens in a single request.
func (c *Client) GetOrderBooks(ctx context.Context, requests []BookRequest) ([]OrderBook, error) {
	var books []OrderBook
	if err := c.post(ctx, "/books", requests, &books); err != nil {
		return nil, fmt.Errorf("get order books: %w", err)
	}
	return books, nil
}
