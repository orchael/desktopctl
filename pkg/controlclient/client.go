package controlclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client holds service authentication, never returned in API models or logs.
// Organization is bound at construction by an authorized server-side caller.
type Client struct {
	base, token, organization string
	http                      *http.Client
}
type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("ai-desktops HTTP %d", e.Status) }
func New(base, token, organization string, h *http.Client) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || organization == "" {
		return nil, errors.New("invalid ai-desktops client configuration")
	}
	if h == nil {
		h = &http.Client{Timeout: 30 * time.Second}
	}
	copy := *h
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: strings.TrimRight(base, "/"), token: token, organization: organization, http: &copy}, nil
}
func (c *Client) Acquire(ctx context.Context, q AcquireRequest) (Lease, error) {
	var l Lease
	if q.OrganizationID != c.organization {
		return l, errors.New("workspace organization mismatch")
	}
	err := c.do(ctx, http.MethodPost, "/api/workspaces/acquire", q, &l)
	return l, err
}
func (c *Client) Get(ctx context.Context, id string) (Lease, error) {
	var l Lease
	err := c.do(ctx, http.MethodGet, "/api/workspaces/"+url.PathEscape(id), nil, &l)
	return l, err
}
func (c *Client) Release(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/workspaces/"+url.PathEscape(id), nil, nil)
}
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return errors.New("invalid control request")
	}
	req.Header.Set("X-Organization-ID", c.organization)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("ai-desktops unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return errors.New("invalid ai-desktops response")
	}
	return nil
}
