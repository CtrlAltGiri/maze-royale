// Package httpx is the tiny JSON-over-HTTP toolkit every service uses to talk
// to its peers: a client with request ids and timeouts, response helpers, a
// request id middleware, and a health handler.
package httpx

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// HeaderRequestID carries a request id across service hops so one keypress
// can be traced through the whole system.
const HeaderRequestID = "X-Request-Id"

type ctxKey struct{}

// NewRequestID returns a random 16 hex char id.
func NewRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// WithRequestID stores an id on the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// RequestIDFrom returns the id on the context, or "".
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

// Error is a non-2xx response decoded from a peer.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("httpx: %d %s: %s", e.Status, e.Code, e.Message)
}

// StatusOf returns the HTTP status behind err, or 0.
func StatusOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

// Client posts and gets JSON against one peer base URL.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient creates a client with a sane timeout for localhost peers.
func NewClient(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: 5 * time.Second}}
}

// Post sends body as JSON to path and decodes the response into out (may be nil).
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

// Get fetches path and decodes the response into out.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("httpx: marshal: %w", err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	id := RequestIDFrom(ctx)
	if id == "" {
		id = NewRequestID()
	}
	req.Header.Set(HeaderRequestID, id)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("httpx: %s %s: %w", method, path, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		e := &Error{Status: res.StatusCode, Code: protocol.ErrInternal, Message: string(data)}
		var he protocol.HTTPError
		if json.Unmarshal(data, &he) == nil && he.Code != "" {
			e.Code, e.Message = he.Code, he.Message
		}
		return e
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("httpx: decode %s %s: %w", method, path, err)
	}
	return nil
}

// WriteJSON writes v with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// WriteError writes a protocol.HTTPError body.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, protocol.HTTPError{Code: code, Message: message})
}

// ReadJSON decodes the request body into v, rejecting unknown fields is NOT
// enforced so the contract can grow additively.
func ReadJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("bad json body: %w", err)
	}
	return nil
}

// NotImplemented is the stub handler every scaffolded route starts with.
func NotImplemented(w http.ResponseWriter, _ *http.Request) {
	WriteError(w, http.StatusNotImplemented, protocol.ErrNotImplement, "not implemented")
}

// Health returns a GET /healthz handler.
func Health(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, protocol.HealthResponse{Service: service, Version: protocol.Version, OK: true})
	}
}

// RequestID is middleware that reads or mints X-Request-Id, stores it on the
// context, echoes it on the response, and logs one line per request.
func RequestID(service string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if id == "" {
			id = NewRequestID()
		}
		w.Header().Set(HeaderRequestID, id)
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r.WithContext(WithRequestID(r.Context(), id)))
		if r.URL.Path != "/healthz" {
			log.Printf("[%s] rid=%s %s %s -> %d (%s)", service, id, r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Microsecond))
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer (needed by
// websocket upgrades through this middleware).
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// ListenAndServe runs srv until ctx is cancelled, then shuts it down gracefully.
func ListenAndServe(ctx context.Context, service string, port int, h http.Handler) error {
	srv := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", port),
		Handler:           RequestID(service, h),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Printf("[%s] listening on http://localhost:%d", service, port)
		errc <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
