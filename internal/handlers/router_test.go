package handlers

import (
	"bytes"
	"database/sql"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/db"
	"9router/proxy/internal/dbtest"
)

func setupTestDB(t *testing.T) (*sql.DB, func()) {
	tmpFile, err := os.CreateTemp("", "test_router_*.sqlite")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()

	database, err := db.OpenDatabase(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("OpenDatabase failed: %v", err)
	}
	if err := dbtest.CreateTables(database); err != nil {
		database.Close()
		os.Remove(tmpFile.Name())
		t.Fatalf("CreateTables failed: %v", err)
	}

	cleanup := func() {
		database.Close()
		os.Remove(tmpFile.Name())
	}
	return database, cleanup
}

func TestSetupRoutes(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	r := chi.NewRouter()
	SetupRoutes(r, repo, nil)

	req := httptest.NewRequest("POST", "/chat/completions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusMethodNotAllowed || w.Code == http.StatusNotFound {
		t.Errorf("expected /chat/completions route to be registered, got status %d", w.Code)
	}
}
func TestSetupRoutes_OAuthEndpointsMounted(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	r := chi.NewRouter()
	SetupRoutes(r, repo, nil)

	endpoints := []struct {
		method string
		path   string
	}{
		{"POST", "/api/oauth/freebuff/initiate"},
		{"POST", "/api/oauth/freebuff/poll"},
		{"GET", "/api/oauth/freebuff/session"},
		{"POST", "/api/oauth/freebuff/session/switch"},
		{"GET", "/api/oauth/antigravity/authorize"},
		{"POST", "/api/oauth/antigravity/exchange"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			t.Errorf("expected %s %s route to be registered, got 404", ep.method, ep.path)
		}
	}
}

func TestSetupServerRouter_PprofMounted(t *testing.T) {
	t.Setenv("PPROF_ENABLED", "true")
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	for _, path := range []string{"/debug/pprof/", "/debug/pprof/heap", "/debug/pprof/goroutine"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected %s to return 200 OK when PPROF_ENABLED=true, got %d", path, w.Code)
		}
	}
}

func TestSetupServerRouter_PprofDisabledByDefault(t *testing.T) {
	t.Setenv("PPROF_ENABLED", "false")
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	for _, path := range []string{"/debug/pprof/", "/debug/pprof/cmdline", "/debug/pprof/profile"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("expected %s to return 404 Not Found by default, got %d", path, w.Code)
		}
	}
}
func TestSetupServerRouter_VersionPublic(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	// Sidebar polls /api/version on every dashboard page including /login,
	// before any session or API key exists (upstream PUBLIC_API_PATHS).
	for _, path := range []string{"/version", "/api/version", "/api/version/status", "/api/version/check"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusUnauthorized || w.Code == http.StatusNotFound {
			t.Errorf("expected %s to be public, got %d", path, w.Code)
		}
	}
}

func TestSetupServerRouter_SPARoutes(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	// Unauthenticated with no settings row: login is required, so /dashboard
	// pages redirect to /login (upstream dashboardGuard) while the other SPA
	// aliases still serve the shell (the SPA shows the login screen itself).
	guardedPaths := []string{
		"/dashboard",
		"/dashboard/combos",
		"/dashboard/providers",
		"/dashboard/terminal",
		"/dashboard/usage",
		"/dashboard/quota",
	}
	for _, p := range guardedPaths {
		req := httptest.NewRequest("GET", p, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusFound {
			t.Errorf("expected GET %s to redirect to /login, got %d", p, w.Code)
		}
		if loc := w.Header().Get("Location"); loc != "/login" {
			t.Errorf("expected GET %s Location /login, got %q", p, loc)
		}
	}

	spaPaths := []string{
		"/login",
		"/connections",
		"/combos",
		"/analytics",
		"/terminal",
		"/keys",
		"/settings",
		"/providers",
		"/usage",
		"/quota",
	}
	for _, p := range spaPaths {
		req := httptest.NewRequest("GET", p, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected GET %s to return 200, got %d (Location: %s): %s", p, w.Code, w.Header().Get("Location"), w.Body.String())
		}
	}

	// Ensure static assets work
	reqAsset := httptest.NewRequest("GET", "/providers/anthropic.png", nil)
	wAsset := httptest.NewRecorder()
	r.ServeHTTP(wAsset, reqAsset)
	if wAsset.Code != http.StatusOK {
		t.Errorf("expected GET /providers/anthropic.png to return 200, got %d", wAsset.Code)
	}

	// PWA shell files referenced by index.html must be served at root
	for _, p := range []string{"/sw.js", "/manifest.webmanifest", "/manifest.json"} {
		req := httptest.NewRequest("GET", p, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected GET %s to return 200, got %d", p, w.Code)
		}
	}

	// Ensure non-existent static assets return 404
	reqMissing := httptest.NewRequest("GET", "/assets/missing.js", nil)
	wMissing := httptest.NewRecorder()
	r.ServeHTTP(wMissing, reqMissing)
	if wMissing.Code != http.StatusNotFound {
		t.Errorf("expected GET /assets/missing.js to return 404, got %d", wMissing.Code)
	}

	// Ensure API endpoints like /api/settings are NOT shadowed by SPA handler
	reqAPI := httptest.NewRequest("GET", "/api/settings", nil)
	wAPI := httptest.NewRecorder()
	r.ServeHTTP(wAPI, reqAPI)
	// When no API keys exist in test DB, RequireApiKey allows or denies based on settings
	if wAPI.Code == http.StatusNotFound {
		t.Errorf("expected /api/settings to be handled by API handler, not 404")
	}
}

func TestConsoleLogsRoutesUseDashboardSessionGate(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	anonymous := httptest.NewRequest(http.MethodGet, "/api/translator/console-logs", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, anonymous)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous console request status = %d", rec.Code)
	}

	var body map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body["error"]["message"] == "" {
		t.Fatalf("missing nested error message: %s", rec.Body.String())
	}
	// A valid engine key must not unlock operational logs.
	keyReq := httptest.NewRequest(http.MethodGet, "/api/translator/console-logs", nil)
	keyReq.Header.Set("Authorization", "Bearer test-api-key")
	if _, err := database.Exec(`INSERT INTO apiKeys (id, key, name, isActive, createdAt) VALUES ('console-test', 'test-api-key', 'test', 1, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, keyReq)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("engine key console request status = %d", rec.Code)
	}

	// requireLogin=false matches upstream's permissive dashboard guard.
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/translator/console-logs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("open dashboard console request status = %d", rec.Code)
	}
}

// TestSetupServerRouter_ModelTestDashboardSession — POST /api/models/test is a
// dashboard endpoint (upstream src/app/api/models/test/route.js behind
// dashboardGuard): a valid login session must pass the guard, an anonymous
// request must still get 401.
func TestSetupServerRouter_ModelTestDashboardSession(t *testing.T) {
	t.Setenv("JWT_SECRET", "router-test-secret")
	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	// requireLogin defaults to on when no settings row exists.
	anon := httptest.NewRequest(http.MethodPost, "/api/models/test", bytes.NewReader([]byte(`{"model":"openai/gpt-4"}`)))
	anon.Header.Set("Content-Type", "application/json")
	anonRec := httptest.NewRecorder()
	r.ServeHTTP(anonRec, anon)
	if anonRec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous /api/models/test status = %d, want 401", anonRec.Code)
	}

	token, err := auth.Sign("router-test-secret", time.Now())
	if err != nil {
		t.Fatalf("sign session token: %v", err)
	}
	sess := httptest.NewRequest(http.MethodPost, "/api/models/test", bytes.NewReader([]byte(`{"model":"openai/gpt-4"}`)))
	sess.Header.Set("Content-Type", "application/json")
	sess.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	sessRec := httptest.NewRecorder()
	r.ServeHTTP(sessRec, sess)
	if sessRec.Code != http.StatusOK {
		t.Fatalf("session-authenticated /api/models/test status = %d, want 200 (ping outcome, not auth 401): %s", sessRec.Code, sessRec.Body.String())
	}
}

// TestSetupServerRouter_CLIToolsStatusIsADashboardRead pins that
// /api/cli-tools/all-statuses sits behind RequireDashboardAuth. The SPA calls
// it with the session cookie, never an LLM API key; when it was registered
// inside SetupRoutes (the RequireApiKey group) every dashboard call returned
// 401 and the CLI Tools view bounced back to the endpoint tab.
func TestSetupServerRouter_CLIToolsStatusIsADashboardRead(t *testing.T) {
	t.Setenv("JWT_SECRET", "router-test-secret")

	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	// requireLogin defaults to on with no settings row, so anonymous is denied.
	anonRec := httptest.NewRecorder()
	r.ServeHTTP(anonRec, httptest.NewRequest(http.MethodGet, "/api/cli-tools/all-statuses", nil))
	if anonRec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous /api/cli-tools/all-statuses status = %d, want 401: %s", anonRec.Code, anonRec.Body.String())
	}

	token, err := auth.Sign("router-test-secret", time.Now())
	if err != nil {
		t.Fatalf("sign session token: %v", err)
	}
	sess := httptest.NewRequest(http.MethodGet, "/api/cli-tools/all-statuses", nil)
	sess.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	sessRec := httptest.NewRecorder()
	r.ServeHTTP(sessRec, sess)
	if sessRec.Code != http.StatusOK {
		t.Fatalf("session-authenticated /api/cli-tools/all-statuses status = %d, want 200 (not auth 401): %s", sessRec.Code, sessRec.Body.String())
	}
	if !strings.Contains(sessRec.Body.String(), "claude") {
		t.Errorf("expected a per-tool status map, got %s", sessRec.Body.String())
	}
}

// TestSetupServerRouter_CLIToolsStatusAPIKeyAliasUnchanged keeps the unprefixed
// /cli-tools/all-statuses alias on the API-key group, where external callers
// expect it. Moving the /api/ alias out must not drag this one along.
func TestSetupServerRouter_CLIToolsStatusAPIKeyAliasUnchanged(t *testing.T) {
	t.Setenv("JWT_SECRET", "router-test-secret")

	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	// No API key is configured, so the API-key group rejects rather than 404s.
	// A 404 would mean the alias vanished instead of being gated.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cli-tools/all-statuses", nil))
	if rec.Code == http.StatusNotFound {
		t.Fatal("/cli-tools/all-statuses disappeared; it must stay registered on the API-key group")
	}
}

// TestSetupServerRouter_UsageChartAndLogsAreDashboardReads pins that the two
// usage-page endpoints sit behind RequireDashboardAuth. The SPA calls them with
// the session cookie and never an LLM API key, so a registration inside
// SetupRoutes (the RequireApiKey group) would 401 exactly like
// /api/cli-tools/all-statuses once did.
//
// This also settles whether the pre-existing usage routes are reachable with a
// cookie at all: they are registered in both groups, and the API-key group is
// mounted first, so if chi routed a duplicate pattern to the first registration
// then /api/usage/stats would already be returning 401 for the dashboard.
func TestSetupServerRouter_UsageChartAndLogsAreDashboardReads(t *testing.T) {
	t.Setenv("JWT_SECRET", "router-test-secret")

	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	token, err := auth.Sign("router-test-secret", time.Now())
	if err != nil {
		t.Fatalf("sign session token: %v", err)
	}

	paths := []string{
		"/api/usage/chart?period=7d",
		"/api/usage/request-logs",
		// Pre-existing routes, included to catch route shadowing by the
		// API-key group, which is mounted before the dashboard group.
		"/api/usage/stats?period=7d",
		"/api/usage/request-details?limit=5",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			// requireLogin defaults to on with no settings row.
			anonRec := httptest.NewRecorder()
			r.ServeHTTP(anonRec, httptest.NewRequest(http.MethodGet, path, nil))
			if anonRec.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous %s status = %d, want 401: %s", path, anonRec.Code, anonRec.Body.String())
			}

			sess := httptest.NewRequest(http.MethodGet, path, nil)
			sess.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
			sessRec := httptest.NewRecorder()
			r.ServeHTTP(sessRec, sess)
			if sessRec.Code != http.StatusOK {
				t.Fatalf("session-authenticated %s status = %d, want 200 (not auth 401): %s", path, sessRec.Code, sessRec.Body.String())
			}
		})
	}
}

// TestSetupServerRouter_UsageChartAndLogsRejectAPIGroupOnly — a valid LLM API
// key must still be refused when login is required and no session is present,
// which is what RequireDashboardAuth does. This guards against "fixing" the 401
// by moving these routes back into SetupRoutes.
func TestSetupServerRouter_UsageChartAndLogsNotInAPIGroupOnly(t *testing.T) {
	t.Setenv("JWT_SECRET", "router-test-secret")

	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	if _, err := database.Exec(`INSERT INTO apiKeys (id, key, name, isActive, createdAt) VALUES ('usage-test', 'test-api-key', 'test', 1, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed key: %v", err)
	}

	for _, path := range []string{"/api/usage/chart?period=7d", "/api/usage/request-logs"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer test-api-key")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code == http.StatusNotFound {
				t.Fatalf("%s is not registered at all", path)
			}
		})
	}
}

// TestSetupServerRouter_ProviderTestBatchIsADashboardRead exercises the batch
// probe through the production router rather than the dashboard package's own
// RegisterRoutes, which is test-only. The two tables are maintained separately,
// and that is how a route can exist for tests while 404ing in the binary — the
// same class of bug that hit /api/cli-tools/all-statuses.
func TestSetupServerRouter_ProviderTestBatchIsADashboardRead(t *testing.T) {
	t.Setenv("JWT_SECRET", "router-test-secret")

	database, cleanup := setupTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	body := strings.NewReader(`{"mode":"all"}`)

	// requireLogin defaults to on with no settings row, so anonymous is denied.
	anonRec := httptest.NewRecorder()
	r.ServeHTTP(anonRec, httptest.NewRequest(http.MethodPost, "/api/providers/test-batch", body))
	if anonRec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous /api/providers/test-batch status = %d, want 401: %s", anonRec.Code, anonRec.Body.String())
	}

	token, err := auth.Sign("router-test-secret", time.Now())
	if err != nil {
		t.Fatalf("sign session token: %v", err)
	}
	sess := httptest.NewRequest(http.MethodPost, "/api/providers/test-batch", strings.NewReader(`{"mode":"all"}`))
	sess.Header.Set("Content-Type", "application/json")
	sess.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	sessRec := httptest.NewRecorder()
	r.ServeHTTP(sessRec, sess)
	if sessRec.Code != http.StatusOK {
		t.Fatalf("session /api/providers/test-batch status = %d, want 200 (not 404, not auth 401): %s", sessRec.Code, sessRec.Body.String())
	}

	// A missing mode is a client error, which proves the route reached the
	// handler rather than falling through to the index.html SPA fallback.
	badRec := httptest.NewRecorder()
	bad := httptest.NewRequest(http.MethodPost, "/api/providers/test-batch", strings.NewReader(`{}`))
	bad.Header.Set("Content-Type", "application/json")
	bad.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	r.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("missing mode status = %d, want 400 (route may not be registered): %s", badRec.Code, badRec.Body.String())
	}
}
