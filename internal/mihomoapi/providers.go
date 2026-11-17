package mihomoapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Provider exposes only non-secret runtime metadata and node names.
type Provider struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updatedAt"`
	Proxies   []Proxy   `json:"proxies"`
}

// Providers lists native collections without returning their URLs or node credentials.
func (c Client) Providers(ctx context.Context) (map[string]Provider, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var response struct {
		Providers map[string]Provider `json:"providers"`
	}
	if err := c.get(ctx, "/providers/proxies", nil, &response); err != nil {
		return nil, fmt.Errorf("Mihomo provider controller is unavailable")
	}
	return response.Providers, nil
}

// UpdateProvider asks the core to download and atomically replace one collection.
func (c Client) UpdateProvider(ctx context.Context, name string) error {
	target := strings.TrimSuffix(c.Base, "/") + "/providers/proxies/" + url.PathEscape(name)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, target, nil)
	if err != nil {
		return fmt.Errorf("cannot create provider update request")
	}
	if c.Secret != "" {
		request.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Mihomo provider update timed out or controller is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		// Core errors can contain subscription tokens and upstream credentials.
		return fmt.Errorf("Mihomo provider update failed (HTTP %d); check subscription access, format and filters", response.StatusCode)
	}
	return nil
}
