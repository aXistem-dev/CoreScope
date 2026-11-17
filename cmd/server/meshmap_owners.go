package main

// Fork-local (meshmap owner): who runs a repeater, from the public node list
// of meshmap.radio-actief.be. Fetched every refreshMinutes into memory, served
// in bulk on GET /api/meshmap/owners; the Repeater info section shows the
// owner's Discord username. Same shape as known_channels_cache.go: never
// blocks startup, fail-soft to the last good list, no DB.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

const (
	defaultMeshmapOwnersURL     = "https://meshmap.radio-actief.be"
	defaultMeshmapOwnersRefresh = time.Hour
	maxMeshmapOwnersBytes       = 10 * 1024 * 1024
	maxMeshmapOwnerNameRunes    = 64
)

// MeshmapOwnersConfig is the fork-local config block "meshmapOwners".
type MeshmapOwnersConfig struct {
	Enabled        bool   `json:"enabled"`
	URL            string `json:"url"`
	RefreshMinutes int    `json:"refreshMinutes"`
}

// resolve returns the base URL and refresh interval, and false when the
// feature is off (nil block or enabled=false).
func (c *MeshmapOwnersConfig) resolve() (string, time.Duration, bool) {
	if c == nil || !c.Enabled {
		return "", 0, false
	}
	url := strings.TrimRight(strings.TrimSpace(c.URL), "/")
	if url == "" {
		url = defaultMeshmapOwnersURL
	}
	refresh := defaultMeshmapOwnersRefresh
	if c.RefreshMinutes > 0 {
		refresh = time.Duration(c.RefreshMinutes) * time.Minute
	}
	return url, refresh, true
}

type meshmapOwner struct {
	Pubkey      string `json:"pubkey"`
	DiscordName string `json:"discordName"`
}

type meshmapOwnersResponse struct {
	Owners    []meshmapOwner `json:"owners"`
	FetchedAt *time.Time     `json:"fetchedAt"`
	Source    string         `json:"source"`
}

// meshmapNode mirrors the fields of /api/v1/belgian-nodes this feature reads.
type meshmapNode struct {
	PublicKey        string  `json:"public_key"`
	Claimed          bool    `json:"claimed"`
	DiscordOwnerName *string `json:"discord_owner_name"`
}

var meshmapPubkeyRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// parseMeshmapOwners keeps claimed nodes with a valid key and a visible
// owner name, sorted by pubkey.
func parseMeshmapOwners(raw []byte) ([]meshmapOwner, error) {
	var nodes []meshmapNode
	if err := json.Unmarshal(raw, &nodes); err != nil {
		return nil, fmt.Errorf("decode meshmap nodes: %w", err)
	}
	out := make([]meshmapOwner, 0, len(nodes))
	for _, n := range nodes {
		pk := strings.ToLower(strings.TrimSpace(n.PublicKey))
		if !n.Claimed || n.DiscordOwnerName == nil || !meshmapPubkeyRe.MatchString(pk) {
			continue
		}
		name := strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, *n.DiscordOwnerName))
		if name == "" {
			continue
		}
		if r := []rune(name); len(r) > maxMeshmapOwnerNameRunes {
			name = string(r[:maxMeshmapOwnerNameRunes])
		}
		out = append(out, meshmapOwner{Pubkey: pk, DiscordName: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pubkey < out[j].Pubkey })
	return out, nil
}

type meshmapOwnersSnap struct {
	owners    []meshmapOwner
	fetchedAt time.Time
}

type meshmapOwnersCache struct {
	ptr     atomic.Pointer[meshmapOwnersSnap]
	baseURL string
	refresh time.Duration
	client  *http.Client
	// retryBase is the first wait before retrying a failed first fetch; it
	// doubles per attempt, capped at refresh (tests shorten it).
	retryBase time.Duration

	mu      sync.Mutex // guards etag and failing (fetchOnce runs from one goroutine, tests call it directly)
	etag    string
	failing bool
}

func newMeshmapOwnersCache(baseURL string, refresh time.Duration) *meshmapOwnersCache {
	if refresh <= 0 {
		refresh = defaultMeshmapOwnersRefresh
	}
	return &meshmapOwnersCache{
		baseURL:   strings.TrimRight(baseURL, "/"),
		refresh:   refresh,
		client:    &http.Client{Timeout: 30 * time.Second},
		retryBase: time.Minute,
	}
}

func (c *meshmapOwnersCache) snapshot() meshmapOwnersResponse {
	resp := meshmapOwnersResponse{Owners: []meshmapOwner{}, Source: c.baseURL}
	if s := c.ptr.Load(); s != nil {
		resp.Owners = s.owners
		at := s.fetchedAt
		resp.FetchedAt = &at
	}
	return resp
}

// fetchOnce does one conditional fetch; on any failure the previous list stays.
func (c *meshmapOwnersCache) fetchOnce(ctx context.Context) error {
	err := c.fetch(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil && !c.failing {
		log.Printf("[meshmap] owner list fetch failed, keeping the last list: %v", err)
	} else if err == nil && c.failing {
		log.Printf("[meshmap] owner list fetch recovered")
	}
	c.failing = err != nil
	return err
}

func (c *meshmapOwnersCache) fetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/belgian-nodes", nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "CoreScope-MeshmapOwners/1.0 (+https://github.com/efiten/CoreScope)")
	c.mu.Lock()
	if c.etag != "" && c.ptr.Load() != nil {
		req.Header.Set("If-None-Match", c.etag)
	}
	c.mu.Unlock()
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		if s := c.ptr.Load(); s != nil {
			c.ptr.Store(&meshmapOwnersSnap{owners: s.owners, fetchedAt: time.Now().UTC()})
		}
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("meshmap status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMeshmapOwnersBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxMeshmapOwnersBytes {
		return errors.New("meshmap node list larger than 10 MB")
	}
	owners, err := parseMeshmapOwners(body)
	if err != nil {
		return err
	}
	c.ptr.Store(&meshmapOwnersSnap{owners: owners, fetchedAt: time.Now().UTC()})
	c.mu.Lock()
	c.etag = resp.Header.Get("ETag")
	c.mu.Unlock()
	return nil
}

// run starts the background refresh: one fetch now, retried with backoff
// (retryBase, doubling, capped at refresh) until the first success, then
// one every refresh. Without the retries a restart during a meshmap hiccup
// would show no owners for a whole refresh interval.
func (c *meshmapOwnersCache) run(ctx context.Context) {
	go func() {
		wait := c.retryBase
		for c.fetchOnce(ctx) != nil && c.ptr.Load() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if wait *= 2; wait > c.refresh {
				wait = c.refresh
			}
		}
		t := time.NewTicker(c.refresh)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = c.fetchOnce(ctx)
			}
		}
	}()
}

// handleMeshmapOwners — GET /api/meshmap/owners. 404 when the feature is off
// (the route is registered before the config-driven caches are built).
func (s *Server) handleMeshmapOwners(w http.ResponseWriter, r *http.Request) {
	if s.meshmapOwners == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, s.meshmapOwners.snapshot())
}
