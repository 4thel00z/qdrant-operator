// Package qdrant is the net/http client of a Qdrant node's REST API.
package qdrant

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/settings"
)

const (
	readyTimeout    = 5 * time.Second
	errorBodyLimit  = 64 * 1024
	apiKeyHeader    = "api-key"
	jsonContentType = "application/json"
)

// Client talks to any node it is handed; it holds no connection, so a node whose certificate or address changes is never stale.
type Client struct {
	timeout         time.Duration
	snapshotTimeout time.Duration
}

var _ ports.Qdrant = (*Client)(nil)

func New(config settings.Qdrant) (*Client, error) {
	if config.Timeout <= 0 || config.SnapshotTimeout <= 0 {
		return nil, fmt.Errorf("qdrant timeouts must be positive, got %s and %s", config.Timeout, config.SnapshotTimeout)
	}
	return &Client{timeout: config.Timeout, snapshotTimeout: config.SnapshotTimeout}, nil
}

type request struct {
	node    domain.QdrantNode
	timeout time.Duration
	method  string
	path    string
	query   url.Values
	body    any
}

func route(segments ...string) string {
	escaped := make([]string, 0, len(segments))
	for _, segment := range segments {
		escaped = append(escaped, url.PathEscape(segment))
	}
	return "/" + strings.Join(escaped, "/")
}

func waiting() url.Values {
	return url.Values{"wait": {"true"}}
}

func httpClient(node domain.QdrantNode) (*http.Client, error) {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DisableKeepAlives: true}
	if node.CACert != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(node.CACert)) {
			return nil, errors.New("CA certificate holds no PEM certificate")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: transport}, nil
}

// send performs the request and returns the response whatever its status; the caller must call cancel once done with the body.
func send(ctx context.Context, r request) (*http.Response, context.CancelFunc, error) {
	client, err := httpClient(r.node)
	if err != nil {
		return nil, nil, fmt.Errorf("qdrant at %s: %w", r.node.URL, err)
	}
	target := strings.TrimRight(r.node.URL, "/") + r.path
	if len(r.query) > 0 {
		target += "?" + r.query.Encode()
	}
	payload := []byte(nil)
	if r.body != nil {
		payload, err = json.Marshal(r.body)
		if err != nil {
			return nil, nil, fmt.Errorf("qdrant %s %s: encode body: %w", r.method, r.path, err)
		}
	}
	timed, cancel := context.WithTimeout(ctx, r.timeout)
	request, err := http.NewRequestWithContext(timed, r.method, target, bytes.NewReader(payload))
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("qdrant %s %s: %w", r.method, r.path, err)
	}
	if r.body != nil {
		request.Header.Set("Content-Type", jsonContentType)
	}
	if r.node.APIKey != "" {
		request.Header.Set(apiKeyHeader, r.node.APIKey)
	}
	response, err := client.Do(request)
	if err != nil {
		cancel()
		if ctx.Err() != nil {
			return nil, nil, fmt.Errorf("qdrant %s %s on %s: %w", r.method, r.path, r.node.URL, ctx.Err())
		}
		return nil, nil, fmt.Errorf("qdrant at %s is unreachable: %w: %w", r.node.URL, ports.ErrUnreachable, err)
	}
	return response, cancel, nil
}

func replyError(response *http.Response) error {
	raw, err := io.ReadAll(io.LimitReader(response.Body, errorBodyLimit))
	message := errorMessage(raw)
	if message == "" && err != nil {
		message = http.StatusText(response.StatusCode)
	}
	return &ports.QdrantError{Status: response.StatusCode, Message: message}
}

// errorMessage reads {"status":{"error":"..."}}, a plain {"status":"..."} or falls back to the trimmed body.
func errorMessage(raw []byte) string {
	envelope := struct {
		Status json.RawMessage `json:"status"`
	}{}
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Status) > 0 {
		detail := struct {
			Error string `json:"error"`
		}{}
		if err := json.Unmarshal(envelope.Status, &detail); err == nil && detail.Error != "" {
			return detail.Error
		}
		text := ""
		if err := json.Unmarshal(envelope.Status, &text); err == nil && text != "" {
			return text
		}
	}
	return strings.TrimSpace(string(raw))
}

func failed(r request, err error) error {
	return fmt.Errorf("qdrant %s %s on %s: %w", r.method, r.path, r.node.URL, err)
}

// execute runs the request and discards a successful reply's body.
func (c *Client) execute(ctx context.Context, r request) error {
	response, cancel, err := send(ctx, r)
	if err != nil {
		return err
	}
	defer cancel()
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return failed(r, replyError(response))
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return failed(r, err)
	}
	return nil
}

// call runs the request and decodes the "result" of the reply; numbers inside free-form objects stay json.Number.
func call[T any](ctx context.Context, r request) (T, error) {
	var result T
	response, cancel, err := send(ctx, r)
	if err != nil {
		return result, err
	}
	defer cancel()
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return result, failed(r, replyError(response))
	}
	envelope := struct {
		Result json.RawMessage `json:"result"`
	}{}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return result, failed(r, fmt.Errorf("decode reply: %w", err))
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.Result))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return result, failed(r, fmt.Errorf("decode result: %w", err))
	}
	return result, nil
}

func (c *Client) plain(node domain.QdrantNode, method string, path string) request {
	return request{node: node, timeout: c.timeout, method: method, path: path}
}

func (c *Client) slow(node domain.QdrantNode, method string, path string) request {
	return request{node: node, timeout: c.snapshotTimeout, method: method, path: path}
}

func (c *Client) Ready(ctx context.Context, node domain.QdrantNode) bool {
	r := request{node: node, timeout: readyTimeout, method: http.MethodGet, path: "/readyz"}
	response, cancel, err := send(ctx, r)
	if err != nil {
		return false
	}
	defer cancel()
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	defer c.cancel()
	return c.ReadCloser.Close()
}
