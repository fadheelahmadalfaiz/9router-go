package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Claude quota port of open-sse/services/usage/claude.js. Before this there was
// no claude case in fetchProviderUsage, so every Claude Code account answered
// the empty lock fallback and the tracker showed no quota at all.

const claudeOAuthPayload = `{
  "five_hour": {"utilization": 29, "resets_at": "2030-01-01T00:00:00Z"},
  "seven_day": {"utilization": 4, "resets_at": 1893456000},
  "seven_day_opus": {"utilization": 96},
  "extra_usage": {"enabled": true}
}`

// clearClaudeUsageState wipes the per-token cache and cooldown maps. They are
// process-wide by design, so a test that seeds a token has to leave it clean.
func clearClaudeUsageState() {
	claudeCacheMu.Lock()
	claudeCache = map[string]claudeCacheEntry{}
	claudeCacheMu.Unlock()
	claudeCoolMu.Lock()
	claudeCool = map[string]time.Time{}
	claudeCoolMu.Unlock()
}

func resetClaudeUsageState(t *testing.T) {
	t.Helper()
	clearClaudeUsageState()
	t.Cleanup(clearClaudeUsageState)
}

// utilization is a percentage USED, so 29/100 must render as 71% remaining.
// Reading the field as a remaining value doubles the reported usage.
func TestFetchClaudeUsage_UtilizationIsPercentUsed(t *testing.T) {
	resetClaudeUsageState(t)
	withClaudeOAuthServer(t, http.StatusOK, claudeOAuthPayload)

	res := fetchClaudeUsage(t.Context(), "tok", false)
	if res.message != "" {
		t.Fatalf("unexpected message: %q", res.message)
	}
	if res.plan != "Claude Code" {
		t.Errorf("plan = %q, want %q", res.plan, "Claude Code")
	}

	session := claudeRow(t, res, "session (5h)")
	claudeAssertNum(t, session, "used", 29)
	claudeAssertNum(t, session, "total", 100)
	claudeAssertNum(t, session, "remaining", 71)
	claudeAssertNum(t, session, "remainingPercentage", 71)
	if got := claudeRowResetAt(session); got != "2030-01-01T00:00:00Z" {
		t.Errorf("session resetAt = %q, want the ISO value", got)
	}

	// A unix-seconds reset has to come back as RFC3339, not as a raw number.
	weekly := claudeRow(t, res, "weekly (7d)")
	claudeAssertNum(t, weekly, "used", 4)
	claudeAssertNum(t, weekly, "remainingPercentage", 96)
	if got := claudeRowResetAt(weekly); got != "2030-01-01T00:00:00Z" {
		t.Errorf("weekly resetAt = %q, want the unix seconds converted", got)
	}
}

// seven_day_<model> windows become their own rows; a window without a numeric
// utilization is skipped rather than rendered as 0% used.
func TestFetchClaudeUsage_ModelSpecificWeeklyWindows(t *testing.T) {
	resetClaudeUsageState(t)
	withClaudeOAuthServer(t, http.StatusOK, `{
	  "seven_day": {"utilization": 4},
	  "seven_day_opus": {"utilization": 96},
	  "seven_day_opus_plan_mode": {"resets_at": "2030-01-01T00:00:00Z"},
	  "extra_usage": null
	}`)

	res := fetchClaudeUsage(t.Context(), "tok", false)
	row := claudeRow(t, res, "weekly opus (7d)")
	claudeAssertNum(t, row, "used", 96)
	claudeAssertNum(t, row, "remaining", 4)
	if _, exists := res.quotas["weekly opus plan mode (7d)"]; exists {
		t.Errorf("a window without utilization must not become a row: %v", res.quotas)
	}
	if _, exists := res.quotas["weekly  (7d)"]; exists {
		t.Errorf("the bare seven_day window must not be duplicated: %v", res.quotas)
	}
}

// Anthropic 429s the quota endpoint well before it 429s chat, so a read inside
// the TTL must be served from cache instead of hitting the network again.
func TestFetchClaudeUsage_CachesByToken(t *testing.T) {
	resetClaudeUsageState(t)

	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_, _ = w.Write([]byte(claudeOAuthPayload))
	}))
	t.Cleanup(srv.Close)
	withClaudeURLs(t, srv.URL, srv.URL, srv.URL)

	first := fetchClaudeUsage(t.Context(), "tok", false)
	second := fetchClaudeUsage(t.Context(), "tok", false)
	if len(first.quotas) == 0 || len(second.quotas) == 0 {
		t.Fatalf("expected quota rows on both reads: %v / %v", first.quotas, second.quotas)
	}
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 1 {
		t.Errorf("provider was called %d times, want 1 (second read served from cache)", got)
	}

	// A manual refresh must actually re-read.
	fetchClaudeUsage(t.Context(), "tok", true)
	mu.Lock()
	got = calls
	mu.Unlock()
	if got != 2 {
		t.Errorf("provider was called %d times after a forced refresh, want 2", got)
	}
}

// The tracker refreshes every visible connection each tick, so concurrent
// readers of the same token have to collapse onto one upstream request.
func TestFetchClaudeUsage_CollapsesConcurrentReaders(t *testing.T) {
	resetClaudeUsageState(t)

	var mu sync.Mutex
	calls := 0
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release
		_, _ = w.Write([]byte(claudeOAuthPayload))
	}))
	t.Cleanup(srv.Close)
	withClaudeURLs(t, srv.URL, srv.URL, srv.URL)

	results := make([]usageResult, 6)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = fetchClaudeUsage(t.Context(), "tok", false)
		}()
	}
	// Give the goroutines time to pile up on the in-flight call.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 1 {
		t.Errorf("provider was called %d times, want 1 for %d concurrent readers", got, len(results))
	}
	for i, res := range results {
		if len(res.quotas) == 0 {
			t.Errorf("reader %d got no quota rows: %q", i, res.message)
		}
	}
}

// A soft failure must not overwrite a good read on screen.
func TestFetchClaudeUsage_KeepsLastGoodReadOnSoftFailure(t *testing.T) {
	resetClaudeUsageState(t)

	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			_, _ = w.Write([]byte(claudeOAuthPayload))
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	withClaudeURLs(t, srv.URL, srv.URL, srv.URL)

	first := fetchClaudeUsage(t.Context(), "tok", false)
	if len(first.quotas) == 0 {
		t.Fatalf("expected quota rows on the first read: %q", first.message)
	}

	forced := fetchClaudeUsage(t.Context(), "tok", true)
	if len(forced.quotas) == 0 {
		t.Errorf("a 429 replaced the good read with a message: %q", forced.message)
	}
}

// A 429 on the OAuth endpoint cools that token down and drops it on the org
// path, instead of retrying the endpoint that just refused.
func TestFetchClaudeUsage_429CoolsDownAndUsesOrgPath(t *testing.T) {
	resetClaudeUsageState(t)

	var mu sync.Mutex
	oauthCalls, settingsCalls := 0, 0
	mux := http.NewServeMux()
	// One catch-all: the org id lands in the path, so routing on a fixed
	// pattern would miss it.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/oauth/usage"):
			oauthCalls++
			mu.Unlock()
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{}`))
		case strings.HasPrefix(r.URL.Path, "/settings"):
			settingsCalls++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"organization_id":"org 1","organization_name":"Acme","plan":"max"}`))
		default:
			mu.Unlock()
			if got := r.URL.EscapedPath(); got != "/org/org%201/usage" {
				t.Errorf("org id was not escaped into the path: %q", got)
			}
			_, _ = w.Write([]byte(`{"session_5h":{"used":3,"total":100}}`))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	withClaudeURLs(t, srv.URL+"/oauth/usage", srv.URL+"/settings", srv.URL+"/org/%s/usage")

	first := fetchClaudeUsage(t.Context(), "tok", false)
	if first.plan != "max" {
		t.Errorf("plan = %q, want %q from the settings endpoint", first.plan, "max")
	}
	if len(first.quotas) == 0 {
		t.Fatalf("expected org quota rows, got %q", first.message)
	}
	orgRow, ok := first.quotas["session_5h"].(map[string]any)
	if !ok {
		t.Fatalf("expected the org usage payload to pass through, got %v", first.quotas)
	}
	claudeAssertNum(t, orgRow, "used", 3)

	// Inside the cooldown the OAuth endpoint must not be tried again.
	fetchClaudeUsage(t.Context(), "tok", true)
	mu.Lock()
	gotOAuth, gotSettings := oauthCalls, settingsCalls
	mu.Unlock()
	if gotOAuth != 1 {
		t.Errorf("oauth endpoint was called %d times, want 1 (cooldown should suppress it)", gotOAuth)
	}
	if gotSettings < 2 {
		t.Errorf("org path was used %d times, want it to take over from the oauth endpoint", gotSettings)
	}
}

func TestFetchClaudeUsage_LegacyWithoutAdminAccess(t *testing.T) {
	resetClaudeUsageState(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/usage", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"plan":"pro"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	withClaudeURLs(t, srv.URL+"/oauth/usage", srv.URL+"/settings", srv.URL+"/org/%s/usage")

	res := fetchClaudeUsage(t.Context(), "tok", false)
	if !strings.Contains(res.message, "require admin access") {
		t.Errorf("message = %q, want it to name the admin requirement", res.message)
	}
	if res.plan != "pro" {
		t.Errorf("plan = %q, want pro", res.plan)
	}
	if len(res.quotas) != 0 {
		t.Errorf("expected no quota rows, got %v", res.quotas)
	}
}

func TestFetchClaudeUsage_SettingsRejected(t *testing.T) {
	resetClaudeUsageState(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/usage", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	withClaudeURLs(t, srv.URL+"/oauth/usage", srv.URL+"/settings", srv.URL+"/org/%s/usage")

	res := fetchClaudeUsage(t.Context(), "tok", false)
	if !strings.Contains(res.message, "requires admin permissions") {
		t.Errorf("message = %q, want it to name the permission problem", res.message)
	}
}

func TestFetchClaudeUsage_RequiresToken(t *testing.T) {
	resetClaudeUsageState(t)
	res := fetchClaudeUsage(t.Context(), "  ", false)
	if !strings.Contains(res.message, "access token not available") {
		t.Errorf("message = %q, want it to name the missing token", res.message)
	}
}

func TestFetchProviderUsage_DispatchesClaude(t *testing.T) {
	resetClaudeUsageState(t)
	withClaudeOAuthServer(t, http.StatusOK, claudeOAuthPayload)

	res, ok := fetchProviderUsage(t.Context(), "claude", map[string]any{"accessToken": "tok"}, false)
	if !ok {
		t.Fatal("expected fetchProviderUsage to handle claude")
	}
	if _, has := res.quotas["session (5h)"]; !has {
		t.Errorf("expected a session (5h) row, got %v", res.quotas)
	}
}

// End-to-end through the dashboard route, including the ?force=1 the per-row
// refresh button sends.
func TestHandleGetConnectionUsage_ClaudeReturnsRealQuota(t *testing.T) {
	fastQuotaGate(t)
	resetClaudeUsageState(t)

	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_, _ = w.Write([]byte(claudeOAuthPayload))
	}))
	t.Cleanup(srv.Close)
	withClaudeURLs(t, srv.URL, srv.URL, srv.URL)

	repo, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	if err := repo.CreateProviderConnectionFull(
		"cc-1", "claude", "oauth", "cc-1", nil, `{"accessToken":"tok"}`,
	); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	router := setupTestRouter(repo)

	read := func(url string) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
		return body
	}

	body := read("/api/usage/cc-1")
	quotas, ok := body["quotas"].(map[string]any)
	if !ok {
		t.Fatalf("expected a quotas object, got %v", body)
	}
	row, ok := quotas["session (5h)"].(map[string]any)
	if !ok {
		t.Fatalf("expected a session (5h) row, got %v", quotas)
	}
	claudeAssertNum(t, row, "used", 29)
	claudeAssertNum(t, row, "remainingPercentage", 71)

	// The per-row refresh button sends ?force=1 and must re-read upstream.
	read("/api/usage/cc-1?force=1")
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 2 {
		t.Errorf("provider was called %d times, want 2 (force=1 must bypass the cache)", got)
	}
}

// ---------- helpers ----------

func withClaudeURLs(t *testing.T, oauthURL, settingsURL, orgUsageURL string) {
	t.Helper()
	prevOAuth, prevSettings, prevOrg := claudeOAuthUsageURL, claudeSettingsURL, claudeOrgUsageURL
	claudeOAuthUsageURL, claudeSettingsURL, claudeOrgUsageURL = oauthURL, settingsURL, orgUsageURL
	t.Cleanup(func() {
		claudeOAuthUsageURL, claudeSettingsURL, claudeOrgUsageURL = prevOAuth, prevSettings, prevOrg
	})
}

func withClaudeOAuthServer(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer tok")
		}
		if got := r.Header.Get("anthropic-beta"); got != "oauth-2025-04-20" {
			t.Errorf("anthropic-beta = %q, want the oauth beta header", got)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	withClaudeURLs(t, srv.URL, srv.URL+"/settings", srv.URL+"/organizations/%s/usage")
}

func claudeRow(t *testing.T, res usageResult, key string) map[string]any {
	t.Helper()
	row, ok := res.quotas[key].(map[string]any)
	if !ok {
		t.Fatalf("expected a %q row, got %v", key, res.quotas)
	}
	return row
}

func claudeAssertNum(t *testing.T, row map[string]any, key string, want float64) {
	t.Helper()
	got, ok := row[key].(float64)
	if !ok {
		t.Fatalf("expected numeric %q, got %v (%T)", key, row[key], row[key])
	}
	if got != want {
		t.Errorf("%s = %v, want %v", key, got, want)
	}
}

func claudeRowResetAt(row map[string]any) string {
	s, _ := row["resetAt"].(string)
	return s
}
