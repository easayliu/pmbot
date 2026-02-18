package clob

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// CreateAPIKey creates a new API key using L1 authentication.
func (c *Client) CreateAPIKey(ctx context.Context) (*APICredentials, error) {
	if c.privateKey == nil {
		return nil, ErrNoPrivateKey
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig, err := signClobAuth(c.privateKey, c.address, timestamp, 0, c.chainID)
	if err != nil {
		return nil, fmt.Errorf("sign clob auth: %w", err)
	}

	headers := map[string]string{
		"POLY_ADDRESS":   c.address.Hex(),
		"POLY_SIGNATURE": sig,
		"POLY_TIMESTAMP": timestamp,
		"POLY_NONCE":     "0",
	}

	var creds APICredentials
	if err := c.doRequest(ctx, "POST", "/auth/api-key", headers, nil, &creds); err != nil {
		return nil, fmt.Errorf("create api key: %w", err)
	}

	c.credentials = &creds
	return &creds, nil
}

// CreateOrDeriveAPIKey tries to create a new API key, falling back to deriving an existing one.
func (c *Client) CreateOrDeriveAPIKey(ctx context.Context) (*APICredentials, error) {
	creds, err := c.CreateAPIKey(ctx)
	if err != nil {
		slog.Debug("create api key failed, trying derive", "err", err)
		return c.DeriveAPIKey(ctx, 0)
	}
	return creds, nil
}

// DeriveAPIKey derives an existing API key using L1 authentication with the original nonce.
func (c *Client) DeriveAPIKey(ctx context.Context, nonce int) (*APICredentials, error) {
	if c.privateKey == nil {
		return nil, ErrNoPrivateKey
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig, err := signClobAuth(c.privateKey, c.address, timestamp, nonce, c.chainID)
	if err != nil {
		return nil, fmt.Errorf("sign clob auth: %w", err)
	}

	headers := map[string]string{
		"POLY_ADDRESS":   c.address.Hex(),
		"POLY_SIGNATURE": sig,
		"POLY_TIMESTAMP": timestamp,
		"POLY_NONCE":     strconv.Itoa(nonce),
	}

	var creds APICredentials
	if err := c.doRequest(ctx, "GET", "/auth/derive-api-key", headers, nil, &creds); err != nil {
		return nil, fmt.Errorf("derive api key: %w", err)
	}

	c.credentials = &creds
	return &creds, nil
}

// buildL2Headers builds HMAC-SHA256 authenticated headers for L2 requests.
func (c *Client) buildL2Headers(method, path string, body string) (map[string]string, error) {
	if c.credentials == nil {
		return nil, ErrNoCredentials
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig, err := buildHMACSignature(c.credentials.Secret, timestamp, method, path, body)
	if err != nil {
		return nil, fmt.Errorf("build hmac signature: %w", err)
	}

	return map[string]string{
		"POLY_ADDRESS":    c.address.Hex(),
		"POLY_SIGNATURE":  sig,
		"POLY_TIMESTAMP":  timestamp,
		"POLY_API_KEY":    c.credentials.Key,
		"POLY_PASSPHRASE": c.credentials.Passphrase,
	}, nil
}

// buildHMACSignature computes the HMAC-SHA256 signature for L2 authentication.
func buildHMACSignature(secret, timestamp, method, path, body string) (string, error) {
	secretBytes, err := base64.URLEncoding.DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("decode secret: %w", err)
	}

	message := timestamp + method + path
	if body != "" {
		message += body
	}

	mac := hmac.New(sha256.New, secretBytes)
	mac.Write([]byte(message))
	return base64.URLEncoding.EncodeToString(mac.Sum(nil)), nil
}
