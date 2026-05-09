package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Session represents an ai-agent-bridge session.
type Session struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Repo     string `json:"repo"`
	Status   string `json:"status"`
}

// ProviderInfo describes an available agent provider.
type ProviderInfo struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

// Client is a typed client for the ai-agent-bridge HTTP API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a Client pointed at the given bridge base URL.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Status returns the bridge liveness status.
func (c *Client) Status(ctx context.Context) (map[string]any, error) {
	return c.get(ctx, "/status")
}

// Providers returns the list of configured agent providers.
func (c *Client) Providers(ctx context.Context) ([]ProviderInfo, error) {
	return getSlice[ProviderInfo](ctx, c, "/providers")
}

// StartSession starts a new agent session.
func (c *Client) StartSession(ctx context.Context, provider, repo string) (*Session, error) {
	body := map[string]string{"provider": provider, "repo": repo}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/sessions", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bridge start session: %w", err)
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("bridge HTTP %d: %s", resp.StatusCode, string(rb))
	}
	var s Session
	if err := json.Unmarshal(rb, &s); err != nil {
		return nil, fmt.Errorf("decode session response: %w", err)
	}
	return &s, nil
}

// StopSession stops a running session.
func (c *Client) StopSession(ctx context.Context, sessionID string) error {
	_, err := c.post(ctx, fmt.Sprintf("/sessions/%s/stop", sessionID), nil)
	return err
}

// ListSessions returns all active sessions.
func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	return getSlice[Session](ctx, c, "/sessions")
}

// getSlice is a generic helper that GETs a JSON array endpoint.
func getSlice[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bridge request GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("bridge HTTP %d: %s", resp.StatusCode, string(b))
	}
	var out []T
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decode bridge response: %w", err)
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, path string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

func (c *Client) post(ctx context.Context, path string, body any) (map[string]any, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	if r != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req)
}

func (c *Client) do(req *http.Request) (map[string]any, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bridge request %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("bridge HTTP %d: %s", resp.StatusCode, string(b))
	}
	var out map[string]any
	if len(b) > 0 {
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, fmt.Errorf("decode bridge response: %w", err)
		}
	}
	return out, nil
}
