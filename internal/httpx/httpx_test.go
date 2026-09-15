package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

type echo struct {
	Msg string `json:"msg"`
	RID string `json:"rid"`
}

func newServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", Health("test"))
	mux.HandleFunc("POST /echo", func(w http.ResponseWriter, r *http.Request) {
		var in echo
		if err := ReadJSON(r, &in); err != nil {
			WriteError(w, http.StatusBadRequest, protocol.ErrBadRequest, err.Error())
			return
		}
		in.RID = RequestIDFrom(r.Context())
		WriteJSON(w, http.StatusOK, in)
	})
	mux.HandleFunc("GET /missing", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, http.StatusNotFound, protocol.ErrNotFound, "no such thing")
	})
	mux.HandleFunc("GET /stub", NotImplemented)
	srv := httptest.NewServer(RequestID("test", mux))
	t.Cleanup(srv.Close)
	return srv
}

func TestPostRoundTripAndRequestID(t *testing.T) {
	srv := newServer(t)
	c := NewClient(srv.URL)
	ctx := WithRequestID(context.Background(), "abc123")
	var out echo
	if err := c.Post(ctx, "/echo", echo{Msg: "hi"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Msg != "hi" || out.RID != "abc123" {
		t.Fatalf("unexpected %+v", out)
	}
}

func TestRequestIDMinted(t *testing.T) {
	srv := newServer(t)
	var out echo
	if err := NewClient(srv.URL).Post(context.Background(), "/echo", echo{Msg: "x"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.RID) != 16 {
		t.Fatalf("expected minted 16 char id, got %q", out.RID)
	}
}

func TestErrorDecoding(t *testing.T) {
	srv := newServer(t)
	c := NewClient(srv.URL)
	err := c.Get(context.Background(), "/missing", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if StatusOf(err) != 404 {
		t.Fatalf("status %d", StatusOf(err))
	}
	if e, ok := err.(*Error); !ok || e.Code != protocol.ErrNotFound || e.Message != "no such thing" {
		t.Fatalf("bad error %v", err)
	}
	err = c.Get(context.Background(), "/stub", nil)
	if StatusOf(err) != http.StatusNotImplemented {
		t.Fatalf("stub should be 501, got %v", err)
	}
}

func TestHealth(t *testing.T) {
	srv := newServer(t)
	var h protocol.HealthResponse
	if err := NewClient(srv.URL).Get(context.Background(), "/healthz", &h); err != nil {
		t.Fatal(err)
	}
	if !h.OK || h.Service != "test" || h.Version != protocol.Version {
		t.Fatalf("bad health %+v", h)
	}
}

func TestBadJSONBody(t *testing.T) {
	srv := newServer(t)
	res, err := http.Post(srv.URL+"/echo", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d", res.StatusCode)
	}
	if res.Header.Get(HeaderRequestID) == "" {
		t.Fatal("response should echo request id")
	}
}
