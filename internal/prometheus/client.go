package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Sample struct {
	Metric map[string]string
	Value  float64
}

type Client struct {
	base string
	http interface {
		Do(*http.Request) (*http.Response, error)
	}
}

func New(endpoint string, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid Prometheus URL %q", endpoint)
	}
	return &Client{base: strings.TrimRight(endpoint, "/"), http: &http.Client{Timeout: timeout}}, nil
}

func (c *Client) Query(ctx context.Context, query string) ([]Sample, error) {
	u := c.base + "/api/v1/query?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Prometheus returned %s", resp.Status)
	}
	var body struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("Prometheus query failed: %s", body.Error)
	}
	if body.Data.ResultType != "vector" {
		return nil, fmt.Errorf("unexpected result type %q", body.Data.ResultType)
	}
	out := make([]Sample, 0, len(body.Data.Result))
	for _, item := range body.Data.Result {
		if len(item.Value) != 2 {
			continue
		}
		var text string
		if err := json.Unmarshal(item.Value[1], &text); err != nil {
			continue
		}
		var v float64
		if _, err := fmt.Sscan(text, &v); err != nil {
			continue
		}
		out = append(out, Sample{Metric: item.Metric, Value: v})
	}
	return out, nil
}
