// Package storage — HTTP helper for cloud sync.
// Uses only the standard library net/http — no external dependency.
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
package storage

import (
	"bytes"
	"context"
	"net/http"
	"time"
)

// httpClient is the shared HTTP client for all cloud sync operations.
// Configured with aggressive timeouts to avoid blocking the sync loop.
var httpClient = &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  false,
		MaxIdleConnsPerHost: 5,
	},
}

// newHTTPRequest creates a new HTTP request with the given body.
func newHTTPRequest(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	var bodyReader *bytes.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	if bodyReader != nil {
		return http.NewRequestWithContext(ctx, method, url, bodyReader)
	}
	return http.NewRequestWithContext(ctx, method, url, nil)
}
