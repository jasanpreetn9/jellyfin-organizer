// Package jellyfin is a minimal REST client for the Jellyfin server API,
// covering only the calls this app needs.
package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	mu      sync.RWMutex
	baseURL string
	apiKey  string
	http    *http.Client
}

func New(baseURL, apiKey string) *Client {
	c := &Client{http: &http.Client{Timeout: 60 * time.Second}}
	c.SetCredentials(baseURL, apiKey)
	return c
}

// SetCredentials swaps the server URL and API key at runtime (settings GUI).
func (c *Client) SetCredentials(baseURL, apiKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.baseURL = strings.TrimRight(baseURL, "/")
	c.apiKey = apiKey
}

func (c *Client) credentials() (baseURL, apiKey string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baseURL, c.apiKey
}

func (c *Client) Configured() bool {
	baseURL, apiKey := c.credentials()
	return baseURL != "" && apiKey != ""
}

func (c *Client) BaseURL() string {
	baseURL, _ := c.credentials()
	return baseURL
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	baseURL, apiKey := c.credentials()
	if baseURL == "" || apiKey == "" {
		return errors.New("Jellyfin URL and API key are not configured (Settings, config.json, or environment)")
	}
	u := baseURL + "/" + strings.TrimLeft(path, "/")
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Emby-Token", apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("jellyfin %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("jellyfin %s %s: read body: %w", method, path, err)
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return fmt.Errorf("jellyfin %s %s: HTTP %d %s", method, path, resp.StatusCode, msg)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("jellyfin %s %s: decode response: %w", method, path, err)
		}
	}
	return nil
}

// ---- Types ----

type User struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

type View struct {
	ID             string `json:"Id"`
	Name           string `json:"Name"`
	CollectionType string `json:"CollectionType"`
}

type Series struct {
	ID             string `json:"Id"`
	Name           string `json:"Name"`
	ProductionYear int    `json:"ProductionYear"`
	Path           string `json:"Path"`
}

type Episode struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Path   string `json:"Path"`
	Season *int   `json:"ParentIndexNumber"`
	Index  *int   `json:"IndexNumber"`
}

type SystemInfo struct {
	ServerName string `json:"ServerName"`
	Version    string `json:"Version"`
}

type itemsPage[T any] struct {
	Items []T `json:"Items"`
}

// ---- Calls ----

func (c *Client) SystemInfo(ctx context.Context) (SystemInfo, error) {
	var info SystemInfo
	err := c.do(ctx, http.MethodGet, "System/Info", nil, nil, &info)
	return info, err
}

func (c *Client) Users(ctx context.Context) ([]User, error) {
	var users []User
	err := c.do(ctx, http.MethodGet, "Users", nil, nil, &users)
	return users, err
}

// Views returns the user's libraries, filtered to those that can hold TV
// shows (tvshows, mixed, folders, or no collection type).
func (c *Client) Views(ctx context.Context, userID string) ([]View, error) {
	var page itemsPage[View]
	if err := c.do(ctx, http.MethodGet, "Users/"+url.PathEscape(userID)+"/Views", nil, nil, &page); err != nil {
		return nil, err
	}
	var views []View
	for _, v := range page.Items {
		switch v.CollectionType {
		case "tvshows", "mixed", "folders", "":
			views = append(views, v)
		}
	}
	return views, nil
}

// pageItems fetches all pages of an /Items query.
func pageItems[T any](ctx context.Context, c *Client, params url.Values) ([]T, error) {
	var all []T
	for start := 0; ; {
		q := url.Values{}
		for k, v := range params {
			q[k] = v
		}
		q.Set("StartIndex", strconv.Itoa(start))
		q.Set("Limit", "200")
		var page itemsPage[T]
		if err := c.do(ctx, http.MethodGet, "Items", q, nil, &page); err != nil {
			return nil, err
		}
		if len(page.Items) == 0 {
			return all, nil
		}
		all = append(all, page.Items...)
		start += len(page.Items)
	}
}

func (c *Client) Series(ctx context.Context, viewID string) ([]Series, error) {
	return pageItems[Series](ctx, c, url.Values{
		"ParentId":         {viewID},
		"IncludeItemTypes": {"Series"},
		"Recursive":        {"true"},
		"Fields":           {"Name,ProductionYear,Path"},
	})
}

// SearchSeries finds series across all libraries by name (server-side fuzzy
// search). Used to map a Sonarr webhook's series title to a Jellyfin item.
func (c *Client) SearchSeries(ctx context.Context, term string) ([]Series, error) {
	return pageItems[Series](ctx, c, url.Values{
		"IncludeItemTypes": {"Series"},
		"Recursive":        {"true"},
		"SearchTerm":       {term},
		"Fields":           {"Name,ProductionYear,Path"},
	})
}

func (c *Client) Episodes(ctx context.Context, seriesID string) ([]Episode, error) {
	return pageItems[Episode](ctx, c, url.Values{
		"ParentId":         {seriesID},
		"IncludeItemTypes": {"Episode"},
		"Recursive":        {"true"},
		"Fields":           {"Path,Name,ParentIndexNumber,IndexNumber"},
	})
}

// ItemForEdit fetches the full item JSON in user context. Mutate the map and
// POST it back with UpdateItem — sending a full payload avoids the 400s that
// partial updates trigger.
func (c *Client) ItemForEdit(ctx context.Context, userID, itemID string) (map[string]any, error) {
	var item map[string]any
	path := "Users/" + url.PathEscape(userID) + "/Items/" + url.PathEscape(itemID)
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &item); err != nil {
		return nil, err
	}
	return item, nil
}

func (c *Client) UpdateItem(ctx context.Context, itemID string, item map[string]any) error {
	return c.do(ctx, http.MethodPost, "Items/"+url.PathEscape(itemID), nil, item, nil)
}

// RefreshItem triggers the metadata savers (NFO sidecar write) without
// re-scraping any metadata or images.
func (c *Client) RefreshItem(ctx context.Context, itemID string) error {
	q := url.Values{
		"Recursive":           {"false"},
		"MetadataRefreshMode": {"None"},
		"ImageRefreshMode":    {"None"},
		"ReplaceAllMetadata":  {"false"},
	}
	return c.do(ctx, http.MethodPost, "Items/"+url.PathEscape(itemID)+"/Refresh", q, nil, nil)
}
