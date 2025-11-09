package chord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

const (
	internalBasePath = "/internal/chord"
)

// HTTPClient implements RPCClient using HTTP endpoints.
type HTTPClient struct {
	client *http.Client
}

// NewHTTPClient builds HTTP Chord client with sane timeouts.
func NewHTTPClient(timeout time.Duration) *HTTPClient {
	return &HTTPClient{
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				MaxIdleConns:        128,
				MaxIdleConnsPerHost: 8,
				IdleConnTimeout:     30 * time.Second,
				TLSHandshakeTimeout: 5 * time.Second,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
			},
		},
	}
}

type findSuccessorRequest struct {
	ID Identifier `json:"id"`
}

type findSuccessorResponse struct {
	Node RemoteNode `json:"node"`
	Hops int        `json:"hops"`
}

func (c *HTTPClient) FindSuccessor(ctx context.Context, address string, id Identifier) (RemoteNode, int, error) {
	reqBody := findSuccessorRequest{ID: id}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(&reqBody); err != nil {
		return RemoteNode{}, 0, err
	}
	url := fmt.Sprintf("http://%s%s/find_successor", address, internalBasePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return RemoteNode{}, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return RemoteNode{}, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return RemoteNode{}, 0, fmt.Errorf("find successor: %s", resp.Status)
	}
	var out findSuccessorResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return RemoteNode{}, 0, err
	}
	return out.Node, out.Hops, nil
}

func (c *HTTPClient) GetSuccessor(ctx context.Context, address string) (RemoteNode, error) {
	url := fmt.Sprintf("http://%s%s/successor", address, internalBasePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return RemoteNode{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return RemoteNode{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return RemoteNode{}, fmt.Errorf("get successor: %s", resp.Status)
	}
	var out struct {
		Node RemoteNode `json:"node"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return RemoteNode{}, err
	}
	return out.Node, nil
}

func (c *HTTPClient) GetPredecessor(ctx context.Context, address string) (RemoteNode, error) {
	url := fmt.Sprintf("http://%s%s/predecessor", address, internalBasePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return RemoteNode{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return RemoteNode{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return RemoteNode{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return RemoteNode{}, fmt.Errorf("get predecessor: %s", resp.Status)
	}
	var out struct {
		Node RemoteNode `json:"node"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return RemoteNode{}, err
	}
	return out.Node, nil
}

func (c *HTTPClient) Notify(ctx context.Context, address string, candidate RemoteNode) error {
	url := fmt.Sprintf("http://%s%s/notify", address, internalBasePath)
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(struct {
		Node RemoteNode `json:"node"`
	}{Node: candidate}); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("notify: %s", resp.Status)
	}
	return nil
}

func (c *HTTPClient) Ping(ctx context.Context, address string) error {
	url := fmt.Sprintf("http://%s%s/ping", address, internalBasePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("ping: %s", resp.Status)
	}
	return nil
}

func (c *HTTPClient) GetSuccessorList(ctx context.Context, address string) ([]RemoteNode, error) {
	url := fmt.Sprintf("http://%s%s/successors", address, internalBasePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("successor list: %s", resp.Status)
	}
	var out struct {
		Nodes []RemoteNode `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Nodes, nil
}

