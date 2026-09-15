package stats

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

func result(matchID string, winners []string, standings ...protocol.Standing) protocol.StatsResultRequest {
	return protocol.StatsResultRequest{MatchID: matchID, EndedAt: 1_700_000_000_000, WinnerIDs: winners, Standings: standings}
}

func st(id, name string, ms int64, place int) protocol.Standing {
	return protocol.Standing{PlayerID: id, Name: name, SurvivedMS: ms, Place: place}
}

func post(t *testing.T, h http.Handler, v any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(v)
	req := httptest.NewRequest(http.MethodPost, "/results", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func board(t *testing.T, h http.Handler, query string) (protocol.LeaderboardResponse, int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/leaderboard"+query, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out protocol.LeaderboardResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return out, rec.Code
}

func TestResultsDedupeAndPersist(t *testing.T) {
	dir := t.TempDir()
	h, err := NewHandlerWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := result("m1", []string{"p1"}, st("p1", "alice", 30000, 1), st("p2", "bob", 12000, 2))
	if rec := post(t, h, r); rec.Code != http.StatusCreated {
		t.Fatalf("first post: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, h, r); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"recorded":false`) {
		t.Fatalf("duplicate post: %d %s", rec.Code, rec.Body)
	}

	data, err := os.ReadFile(filepath.Join(dir, "stats.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Results) != 1 || f.Results[0].MatchID != "m1" {
		t.Fatalf("file contents: %+v", f)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}

	// Reopen: the result is loaded and still deduped.
	h2, err := NewHandlerWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rec := post(t, h2, r); rec.Code != http.StatusOK {
		t.Fatalf("duplicate after reload: %d", rec.Code)
	}
	lb, code := board(t, h2, "")
	if code != http.StatusOK || lb.Total != 2 || lb.Entries[0].Name != "alice" || lb.Entries[0].Wins != 1 {
		t.Fatalf("leaderboard after reload: %d %+v", code, lb)
	}
}

func TestLeaderboardAggregationAndOrder(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(s)
	post(t, h, result("m1", []string{"a"}, st("a", "alice", 30000, 1), st("b", "bob", 10000, 2), st("c", "carol", 20000, 2)))
	post(t, h, result("m2", []string{"b"}, st("a", "alice", 5000, 2), st("b", "bob", 30000, 1), st("c", "carol", 25000, 2)))
	// carol wins with a different player id but the same name; ids are per connection.
	post(t, h, result("m3", []string{"c2"}, st("a", "alice", 1000, 3), st("b", "bob", 2000, 2), st("c2", "carol", 30000, 1)))

	lb, code := board(t, h, "")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if lb.Total != 3 || len(lb.Entries) != 3 {
		t.Fatalf("total=%d entries=%d", lb.Total, len(lb.Entries))
	}
	want := []protocol.LeaderboardEntry{
		{Name: "carol", Wins: 1, Rounds: 3, TotalSurvive: 75000},
		{Name: "bob", Wins: 1, Rounds: 3, TotalSurvive: 42000},
		{Name: "alice", Wins: 1, Rounds: 3, TotalSurvive: 36000},
	}
	for i := range want {
		if lb.Entries[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, lb.Entries[i], want[i])
		}
	}

	page, _ := board(t, h, "?limit=1&offset=1")
	if page.Total != 3 || len(page.Entries) != 1 || page.Entries[0].Name != "bob" {
		t.Fatalf("page: %+v", page)
	}
	past, _ := board(t, h, "?limit=10&offset=99")
	if past.Total != 3 || len(past.Entries) != 0 {
		t.Fatalf("offset past end: %+v", past)
	}
	if _, code := board(t, h, "?limit=x"); code != http.StatusBadRequest {
		t.Fatalf("bad limit status %d", code)
	}
}

func TestResultsValidation(t *testing.T) {
	s, _ := Open(t.TempDir())
	h := Handler(s)
	if rec := post(t, h, result("", nil)); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing match_id: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/results", strings.NewReader("{nope"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
	if s.Len() != 0 {
		t.Fatalf("invalid results were stored")
	}
}

func TestOpenMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(result("m1", nil, st("a", "alice", 1, 1))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stats.json")); err != nil {
		t.Fatal(err)
	}
}
