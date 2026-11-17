package main

// Fork-local (meshmap owner): tests for meshmap_owners.go.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const mmPK = "efef7943505052b47f1809488ea4b4d3942d4ed72d2b1953b90a9f5e62a65fb5"

func TestParseMeshmapOwnersFilters(t *testing.T) {
	long := strings.Repeat("é", 70)
	upper := strings.ToUpper(strings.Repeat("ab", 32))
	raw := `[
	 {"public_key":"` + mmPK + `","claimed":true,"discord_owner_name":"erwinfiten"},
	 {"public_key":"` + strings.Repeat("11", 32) + `","claimed":false,"discord_owner_name":"notclaimed"},
	 {"public_key":"` + strings.Repeat("22", 32) + `","claimed":true,"discord_owner_name":null},
	 {"public_key":"` + strings.Repeat("33", 32) + `","claimed":true,"discord_owner_name":"   "},
	 {"public_key":"` + upper + `","claimed":true,"discord_owner_name":"upper"},
	 {"public_key":"` + strings.Repeat("4", 63) + `","claimed":true,"discord_owner_name":"short"},
	 {"public_key":"` + strings.Repeat("55", 32) + `","claimed":true,"discord_owner_name":"a\nb<i>"},
	 {"public_key":"` + strings.Repeat("66", 32) + `","claimed":true,"discord_owner_name":"` + long + `"}
	]`
	got, err := parseMeshmapOwners([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	want := []meshmapOwner{
		{Pubkey: strings.Repeat("55", 32), DiscordName: "ab<i>"},
		{Pubkey: strings.Repeat("66", 32), DiscordName: strings.Repeat("é", 64)},
		{Pubkey: strings.Repeat("ab", 32), DiscordName: "upper"},
		{Pubkey: mmPK, DiscordName: "erwinfiten"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d owners: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("owner %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseMeshmapOwnersBadJSON(t *testing.T) {
	for _, raw := range []string{`{`, `{"a":1}`, ``} {
		if _, err := parseMeshmapOwners([]byte(raw)); err == nil {
			t.Fatalf("no error for %q", raw)
		}
	}
}

// fakeMeshmap serves /api/v1/belgian-nodes; respond decides each answer.
func fakeMeshmap(t *testing.T, respond func(w http.ResponseWriter, r *http.Request, n int)) *httptest.Server {
	t.Helper()
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/belgian-nodes" {
			http.NotFound(w, r)
			return
		}
		respond(w, r, int(calls.Add(1)))
	}))
	t.Cleanup(ts.Close)
	return ts
}

const oneOwner = `[{"public_key":"` + mmPK + `","claimed":true,"discord_owner_name":"erwinfiten"}]`

func TestMeshmapOwnersFetchETag(t *testing.T) {
	var sentINM string
	ts := fakeMeshmap(t, func(w http.ResponseWriter, r *http.Request, n int) {
		switch n {
		case 1:
			w.Header().Set("ETag", `"v1"`)
			w.Write([]byte(oneOwner))
		case 2:
			sentINM = r.Header.Get("If-None-Match")
			w.WriteHeader(http.StatusNotModified)
		default:
			w.Write([]byte(`[]`))
		}
	})
	c := newMeshmapOwnersCache(ts.URL, time.Hour)
	ctx := context.Background()
	if err := c.fetchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.fetchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sentINM != `"v1"` {
		t.Fatalf("If-None-Match = %q", sentINM)
	}
	if s := c.snapshot(); len(s.Owners) != 1 || s.Owners[0].DiscordName != "erwinfiten" {
		t.Fatalf("304 changed the map: %+v", s.Owners)
	}
	if err := c.fetchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if s := c.snapshot(); len(s.Owners) != 0 {
		t.Fatalf("empty list kept owners: %+v", s.Owners)
	}
}

func TestMeshmapOwnersFetchFailureKeepsMap(t *testing.T) {
	ts := fakeMeshmap(t, func(w http.ResponseWriter, r *http.Request, n int) {
		switch n {
		case 1:
			w.Write([]byte(oneOwner))
		case 2:
			w.WriteHeader(http.StatusInternalServerError)
		case 3:
			w.Write([]byte("[" + strings.Repeat(" ", maxMeshmapOwnersBytes) + "]"))
		default:
			w.Write([]byte(`{`))
		}
	})
	c := newMeshmapOwnersCache(ts.URL, time.Hour)
	ctx := context.Background()
	if err := c.fetchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := c.fetchOnce(ctx); err == nil {
			t.Fatalf("fetch %d: no error", i+2)
		}
		if s := c.snapshot(); len(s.Owners) != 1 {
			t.Fatalf("fetch %d dropped the map: %+v", i+2, s.Owners)
		}
	}
}

func TestMeshmapOwnersRoute(t *testing.T) {
	srv, router := setupTestServer(t)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/meshmap/owners", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled: status %d, want 404", w.Code)
	}

	ts := fakeMeshmap(t, func(w http.ResponseWriter, r *http.Request, n int) { w.Write([]byte(oneOwner)) })
	srv.meshmapOwners = newMeshmapOwnersCache(ts.URL, time.Hour)

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/meshmap/owners", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("before first fetch: status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"owners":[]`) || !strings.Contains(w.Body.String(), `"fetchedAt":null`) {
		t.Fatalf("empty body = %s", w.Body.String())
	}

	if err := srv.meshmapOwners.fetchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/meshmap/owners", nil))
	var resp meshmapOwnersResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Owners) != 1 || resp.Owners[0].Pubkey != mmPK || resp.FetchedAt == nil || resp.Source != ts.URL {
		t.Fatalf("resp = %s", w.Body.String())
	}
}

func TestMeshmapOwnersConfigDefaults(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"meshmapOwners":{"enabled":true}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	url, refresh, ok := cfg.MeshmapOwners.resolve()
	if !ok || url != "https://meshmap.radio-actief.be" || refresh != time.Hour {
		t.Fatalf("defaults = %q %v %v", url, refresh, ok)
	}
	cfg.MeshmapOwners.Enabled = false
	if _, _, ok := cfg.MeshmapOwners.resolve(); ok {
		t.Fatal("disabled config resolved as on")
	}
	var nilCfg *MeshmapOwnersConfig
	if _, _, ok := nilCfg.resolve(); ok {
		t.Fatal("nil config resolved as on")
	}
}
