// Package bmtc scrapes the unofficial Namma BMTC JSON API (the backend of
// the Namma BMTC app and bmtcwebportal.amnex.com) into the SQLite store.
// Every response is stored raw first so normalisation can be re-run after a
// decoder fix without hitting the API again.
package bmtc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sort"
	"time"

	"golang.org/x/time/rate"
)

// DefaultBaseURL is the host the Namma BMTC app talks to.
const DefaultBaseURL = "https://bmtcmobileapi.karnataka.gov.in/WebAPI"

// RawStore receives every response for archival. Implemented by the SQLite
// store; nil disables archival.
type RawStore interface {
	SaveRaw(ctx context.Context, endpoint, paramsHash, paramsJSON string, status int, body []byte, fetchedAt time.Time) error
}

// Client is a polite HTTP client for the API.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Limiter *rate.Limiter
	Raw     RawStore
	// MaxRetries for 429/5xx/network errors. Default 3.
	MaxRetries int
	// RetryBase is the first backoff; doubles per attempt with jitter.
	// Default 1s; tests shrink it.
	RetryBase time.Duration
	// Calls counts requests made (including retries) for run bookkeeping.
	Calls int
}

// NewClient returns a client limited to rps requests per second.
func NewClient(baseURL string, rps float64, raw RawStore) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL:    baseURL,
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		Limiter:    rate.NewLimiter(rate.Limit(rps), 4),
		Raw:        raw,
		MaxRetries: 3,
		RetryBase:  time.Second,
	}
}

// Headers mirror what the web portal sends; the API rejects bare requests.
func headers(req *http.Request) {
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("lan", "en")
	req.Header.Set("deviceType", "WEB")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Origin", "https://bmtcwebportal.amnex.com")
	req.Header.Set("Referer", "https://bmtcwebportal.amnex.com/")
}

// Envelope is the common response wrapper.
type Envelope struct {
	Issuccess    bool            `json:"Issuccess"`
	Message      string          `json:"Message"`
	RowCount     int             `json:"RowCount"`
	Data         json.RawMessage `json:"data"`
	ResponseCode int             `json:"responsecode"`
}

// ErrShape is returned when a response decodes but lacks required fields;
// callers count these to detect API drift.
var ErrShape = errors.New("unexpected response shape")

// Post sends body to endpoint and returns the raw response bytes after
// archiving them. Retries with exponential backoff and jitter.
func (c *Client) Post(ctx context.Context, endpoint string, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	hash := paramsHash(payload)
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			d := time.Duration(1<<attempt)*c.RetryBase + time.Duration(rand.IntN(1000))*c.RetryBase/1000
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if err := c.Limiter.Wait(ctx); err != nil {
			return nil, err
		}
		c.Calls++
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/"+endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		headers(req)
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if c.Raw != nil {
			if err := c.Raw.SaveRaw(ctx, endpoint, hash, string(payload), resp.StatusCode, data, time.Now()); err != nil {
				return nil, fmt.Errorf("archive raw: %w", err)
			}
		}
		switch {
		case resp.StatusCode == http.StatusOK:
			return data, nil
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = fmt.Errorf("%s: HTTP %d", endpoint, resp.StatusCode)
			continue
		default:
			return nil, fmt.Errorf("%s: HTTP %d", endpoint, resp.StatusCode)
		}
	}
	return nil, fmt.Errorf("%s: giving up after %d attempts: %w", endpoint, c.MaxRetries+1, lastErr)
}

// Call posts and decodes the standard envelope's data into out.
func (c *Client) Call(ctx context.Context, endpoint string, body any, out any) error {
	data, err := c.Post(ctx, endpoint, body)
	if err != nil {
		return err
	}
	return DecodeEnvelope(endpoint, data, out)
}

// DecodeEnvelope unwraps the standard envelope. Exposed so normalisation
// can be replayed from archived raw bodies.
func DecodeEnvelope(endpoint string, data []byte, out any) error {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("%s: %w: %v", endpoint, ErrShape, err)
	}
	if !env.Issuccess {
		return fmt.Errorf("%s: api says %q", endpoint, env.Message)
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return fmt.Errorf("%s: %w: no data", endpoint, ErrShape)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("%s: %w: %v", endpoint, ErrShape, err)
	}
	return nil
}

func paramsHash(payload []byte) string {
	// Canonicalise key order so equal params hash equal.
	var m map[string]any
	if json.Unmarshal(payload, &m) == nil {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b bytes.Buffer
		for _, k := range keys {
			v, _ := json.Marshal(m[k])
			b.WriteString(k)
			b.WriteByte('=')
			b.Write(v)
			b.WriteByte(';')
		}
		payload = b.Bytes()
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:8])
}
