// Package upstream calls the public API-key routes (/v1/api/*) of the Uptimy
// services, the same ones uptimyctl uses. The MCP server owns no data: every
// request carries the caller's own upt_ key, so each service authorizes it
// exactly as it would a CLI call.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Service picks which backend a request goes to. Incidents and maintenances
// live on workflows, heartbeats on the heartbeat service, everything else on api.
type Service int

const (
	API Service = iota
	Workflows
	Heartbeats
)

type Client struct {
	http *http.Client
	base map[Service]string
}

func New(apiURL, workflowsURL, heartbeatsURL string) *Client {
	return &Client{
		http: &http.Client{Timeout: 30 * time.Second},
		base: map[Service]string{API: apiURL, Workflows: workflowsURL, Heartbeats: heartbeatsURL},
	}
}

// Error is a non-2xx answer from a service.
type Error struct {
	StatusCode int
	Message    string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.StatusCode)
}

// IsNotFound reports whether err is a 404 from a service.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.StatusCode == http.StatusNotFound
}

// Do sends one request with key as the bearer token and decodes the
// response's "data" field into out (when out is not nil).
func (c *Client) Do(ctx context.Context, key string, svc Service, method, path string, query url.Values, body, out any) error {
	u := c.base[svc] + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "upti.my-mcp")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Error
		if msg == "" {
			msg = e.Message
		}
		return &Error{StatusCode: resp.StatusCode, Message: msg}
	}

	if out == nil || len(raw) == 0 {
		return nil
	}
	var wrapper struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if len(wrapper.Data) == 0 {
		return nil
	}
	return json.Unmarshal(wrapper.Data, out)
}

// Results is the {results: [...]} shape list endpoints return inside "data".
type Results[T any] struct {
	Results []T `json:"results"`
}
