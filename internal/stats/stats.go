// Package stats persists match results to ./data/stats.json and serves the
// leaderboard read model. Port 8094.
package stats

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

const (
	// DefaultDataDir is where stats.json lives unless overridden.
	DefaultDataDir = "./data"
	fileName       = "stats.json"
	defaultLimit   = 50
	maxLimit       = 1000
)

// file is the on-disk shape: an append-only list of results.
type file struct {
	Results []protocol.StatsResultRequest `json:"results"`
}

// Store holds every match result in memory and mirrors it to disk.
type Store struct {
	mu      sync.RWMutex
	dir     string
	results []protocol.StatsResultRequest
	byMatch map[string]struct{}
}

// Open loads dir/stats.json if it exists and returns a Store rooted at dir.
func Open(dir string) (*Store, error) {
	s := &Store{dir: dir, byMatch: map[string]struct{}{}}
	data, err := os.ReadFile(s.path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("stats: read %s: %w", s.path(), err)
	}
	var f file
	if len(data) > 0 {
		if err := json.Unmarshal(data, &f); err != nil {
			return nil, fmt.Errorf("stats: decode %s: %w", s.path(), err)
		}
	}
	for _, r := range f.Results {
		if _, dup := s.byMatch[r.MatchID]; dup {
			continue
		}
		s.byMatch[r.MatchID] = struct{}{}
		s.results = append(s.results, r)
	}
	return s, nil
}

func (s *Store) path() string { return filepath.Join(s.dir, fileName) }

// Add records a result. It returns false (and does not write) when the
// match_id has already been recorded.
func (s *Store) Add(r protocol.StatsResultRequest) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.byMatch[r.MatchID]; dup {
		return false, nil
	}
	s.results = append(s.results, r)
	s.byMatch[r.MatchID] = struct{}{}
	if err := s.flushLocked(); err != nil {
		s.results = s.results[:len(s.results)-1]
		delete(s.byMatch, r.MatchID)
		return false, err
	}
	return true, nil
}

// flushLocked writes the whole file atomically: temp file in the same dir,
// fsync, rename over the target.
func (s *Store) flushLocked() error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("stats: mkdir %s: %w", s.dir, err)
	}
	data, err := json.MarshalIndent(file{Results: s.results}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, fileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("stats: temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(fmt.Errorf("stats: write: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(fmt.Errorf("stats: sync: %w", err))
	}
	if err := tmp.Close(); err != nil {
		return cleanup(fmt.Errorf("stats: close: %w", err))
	}
	if err := os.Rename(tmpName, s.path()); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("stats: rename: %w", err)
	}
	return nil
}

// Len returns the number of recorded matches.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.results)
}

// Leaderboard aggregates every result per player name, sorted by wins desc,
// then total_survived_ms desc, then name asc for a stable order, and returns
// the page [offset, offset+limit) plus the total number of distinct names.
func (s *Store) Leaderboard(limit, offset int) protocol.LeaderboardResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agg := map[string]*protocol.LeaderboardEntry{}
	for _, r := range s.results {
		winners := make(map[string]struct{}, len(r.WinnerIDs))
		for _, id := range r.WinnerIDs {
			winners[id] = struct{}{}
		}
		for _, st := range r.Standings {
			e := agg[st.Name]
			if e == nil {
				e = &protocol.LeaderboardEntry{Name: st.Name}
				agg[st.Name] = e
			}
			e.Rounds++
			e.TotalSurvive += st.SurvivedMS
			if _, won := winners[st.PlayerID]; won {
				e.Wins++
			}
		}
	}
	all := make([]protocol.LeaderboardEntry, 0, len(agg))
	for _, e := range agg {
		all = append(all, *e)
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.Wins != b.Wins {
			return a.Wins > b.Wins
		}
		if a.TotalSurvive != b.TotalSurvive {
			return a.TotalSurvive > b.TotalSurvive
		}
		return a.Name < b.Name
	})

	total := len(all)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	end := offset + limit
	if end > total {
		end = total
	}
	entries := make([]protocol.LeaderboardEntry, end-offset)
	copy(entries, all[offset:end])
	return protocol.LeaderboardResponse{Entries: entries, Total: total}
}

// NewHandler returns the service's HTTP handler backed by DefaultDataDir.
// It is the entry point cmd/stats uses; the data dir is configurable through
// NewHandlerWithDir.
func NewHandler() http.Handler {
	h, err := NewHandlerWithDir(DefaultDataDir)
	if err != nil {
		panic(err)
	}
	return h
}

// NewHandlerWithDir opens dir/stats.json and returns the handler.
func NewHandlerWithDir(dir string) (http.Handler, error) {
	s, err := Open(dir)
	if err != nil {
		return nil, err
	}
	return Handler(s), nil
}

// Handler mounts the routes on top of an opened Store.
func Handler(s *Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Health("stats"))
	mux.HandleFunc("POST /results", s.handleResults)
	mux.HandleFunc("GET /leaderboard", s.handleLeaderboard)
	return mux
}

func (s *Store) handleResults(w http.ResponseWriter, r *http.Request) {
	var req protocol.StatsResultRequest
	if err := httpx.ReadJSON(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, protocol.ErrBadRequest, err.Error())
		return
	}
	if req.MatchID == "" {
		httpx.WriteError(w, http.StatusBadRequest, protocol.ErrBadRequest, "match_id is required")
		return
	}
	for i, st := range req.Standings {
		if st.Name == "" {
			httpx.WriteError(w, http.StatusBadRequest, protocol.ErrBadRequest, fmt.Sprintf("standings[%d].name is required", i))
			return
		}
	}
	added, err := s.Add(req)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, protocol.ErrInternal, err.Error())
		return
	}
	if !added {
		// Idempotent: a replayed result is acknowledged without being recorded twice.
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"match_id": req.MatchID, "recorded": false})
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"match_id": req.MatchID, "recorded": true})
}

func (s *Store) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := queryInt(q.Get("limit"), defaultLimit)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, protocol.ErrBadRequest, "limit must be an integer")
		return
	}
	offset, err := queryInt(q.Get("offset"), 0)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, protocol.ErrBadRequest, "offset must be an integer")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s.Leaderboard(limit, offset))
}

func queryInt(v string, def int) (int, error) {
	if v == "" {
		return def, nil
	}
	return strconv.Atoi(v)
}
