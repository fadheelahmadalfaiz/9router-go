package dashboard

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// MiniMax quota port of open-sse/services/usage/minimax.js. Without a
// minimax case in fetchProviderUsage every MiniMax account answered the empty
// lock fallback, so the tracker said "Account active. No quota limits tracked."

const miniMaxCountPayload = `{
  "base_resp": {"status_code": 0, "status_msg": "success"},
  "model_remains": [
    {
      "model_name": "MiniMax-M*",
      "current_interval_total_count": 100,
      "current_interval_usage_count": 29,
      "current_weekly_total_count": 100,
      "current_weekly_usage_count": 4,
      "remains_time": 600000,
      "weekly_remains_time": 6000000
    },
    {
      "model_name": "Video",
      "current_interval_total_count": 100,
      "current_interval_usage_count": 0,
      "current_weekly_total_count": 100,
      "current_weekly_usage_count": 0
    }
  ]
}`

// M3-era buckets ship a percentage and zero counts, so the counts alone cannot
// tell "healthy" from "empty" — they must not be filtered away.
const miniMaxPercentPayload = `{
  "base_resp": {"status_code": 0, "status_msg": "success"},
  "model_remains": [
    {
      "model_name": "MiniMax-M*",
      "current_interval_remaining_percent": 71,
      "current_weekly_remaining_percent": 96
    }
  ]
}`

func TestFetchMiniMaxUsage_CountBasedQuotas(t *testing.T) {
	withMiniMaxServer(t, miniMaxURLs{paths: []string{"/token_plan/remains"}}, miniMaxCountPayload)

	res := fetchMiniMaxUsage(t.Context(), "key", "minimax")
	if res.message != "" {
		t.Fatalf("unexpected message: %q", res.message)
	}
	if len(res.quotas) != 4 {
		t.Fatalf("expected 4 quota rows, got %d (%v)", len(res.quotas), res.quotas)
	}

	five := miniMaxRow(t, res, "M-series (5h)")
	miniMaxAssertNum(t, five, "used", 29)
	miniMaxAssertNum(t, five, "total", 100)
	miniMaxAssertNum(t, five, "remaining", 71)
	miniMaxAssertNum(t, five, "remainingPercentage", 71)
	if miniMaxRowResetAt(five) == "" {
		t.Error("M-series (5h) is missing a resetAt from remains_time")
	}

	weekly := miniMaxRow(t, res, "M-series (7d)")
	miniMaxAssertNum(t, weekly, "used", 4)
	miniMaxAssertNum(t, weekly, "remainingPercentage", 96)

	video := miniMaxRow(t, res, "Video (5h)")
	miniMaxAssertNum(t, video, "used", 0)
	miniMaxAssertNum(t, video, "remainingPercentage", 100)
}

// On token_plan the count means "used", on coding_plan it means "remaining".
// Swapping them inverts the whole bar, so both directions are pinned.
func TestFetchMiniMaxUsage_CountSemanticsPerEndpoint(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		wantUsed      float64
		wantPct       float64
		wantRemaining float64
	}{
		{name: "token_plan count is used", path: "/v1/token_plan/remains", wantUsed: 29, wantPct: 71, wantRemaining: 71},
		{name: "coding_plan count is remaining", path: "/v1/api/openplatform/coding_plan/remains", wantUsed: 71, wantPct: 29, wantRemaining: 29},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withMiniMaxServer(t, miniMaxURLs{paths: []string{tt.path}}, miniMaxCountPayload)

			res := fetchMiniMaxUsage(t.Context(), "key", "minimax")
			row := miniMaxRow(t, res, "M-series (5h)")
			miniMaxAssertNum(t, row, "used", tt.wantUsed)
			miniMaxAssertNum(t, row, "remainingPercentage", tt.wantPct)
			miniMaxAssertNum(t, row, "remaining", tt.wantRemaining)
		})
	}
}

// A percent-only bucket has no real total, so it is normalized onto a 100 base
// and the synthetic count must be built from the percentage, not from the
// (always zero) count field.
func TestFetchMiniMaxUsage_PercentOnlyBuckets(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantUsed float64
	}{
		{name: "token_plan", path: "/v1/token_plan/remains", wantUsed: 29},
		{name: "coding_plan", path: "/v1/api/openplatform/coding_plan/remains", wantUsed: 29},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withMiniMaxServer(t, miniMaxURLs{paths: []string{tt.path}}, miniMaxPercentPayload)

			res := fetchMiniMaxUsage(t.Context(), "key", "minimax")
			row := miniMaxRow(t, res, "M-series (5h)")
			miniMaxAssertNum(t, row, "total", 100)
			miniMaxAssertNum(t, row, "used", tt.wantUsed)
			// The upstream percentage is authoritative for these buckets.
			miniMaxAssertNum(t, row, "remainingPercentage", 71)
		})
	}
}

func TestFetchMiniMaxUsage_MessagePaths(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantSubstr string
	}{
		{
			name:       "unauthorized",
			status:     http.StatusUnauthorized,
			body:       `{}`,
			wantSubstr: "API key invalid or inactive",
		},
		{
			name:       "upstream auth status code",
			status:     http.StatusOK,
			body:       `{"base_resp":{"status_code":1004,"status_msg":"coding plan not active"}}`,
			wantSubstr: "API key invalid or inactive",
		},
		{
			name:       "auth wording in a 200 body",
			status:     http.StatusOK,
			body:       `{"base_resp":{"status_code":0},"error":"invalid api key"}`,
			wantSubstr: "API key invalid or inactive",
		},
		{
			name:       "upstream non-zero status",
			status:     http.StatusOK,
			body:       `{"base_resp":{"status_code":7,"status_msg":"quota api down"}}`,
			wantSubstr: "quota api down",
		},
		{
			name:       "no models at all",
			status:     http.StatusOK,
			body:       `{"base_resp":{"status_code":0},"model_remains":[]}`,
			wantSubstr: "No quota data was returned",
		},
		{
			name:       "models without any quota signal",
			status:     http.StatusOK,
			body:       `{"base_resp":{"status_code":0},"model_remains":[{"model_name":"Video"}]}`,
			wantSubstr: "No quota data was returned",
		},
		{
			name:       "non-retryable rejection",
			status:     http.StatusBadRequest,
			body:       `{}`,
			wantSubstr: "endpoint error (400)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withMiniMaxServer(t, miniMaxURLs{statuses: []int{tt.status}}, tt.body)

			res := fetchMiniMaxUsage(t.Context(), "key", "minimax")
			if !strings.Contains(res.message, tt.wantSubstr) {
				t.Errorf("message = %q, want it to contain %q", res.message, tt.wantSubstr)
			}
			if len(res.quotas) != 0 {
				t.Errorf("expected no quota rows, got %v", res.quotas)
			}
		})
	}
}

// A 404 on the first host means "this host does not serve the plan endpoint",
// not "this account has no plan" — the next URL in the list must be tried.
func TestFetchMiniMaxUsage_FallsBackToNextURL(t *testing.T) {
	withMiniMaxServer(t, miniMaxURLs{
		paths:    []string{"/first", "/second"},
		statuses: []int{http.StatusNotFound, http.StatusOK},
		bodies:   []string{"", miniMaxCountPayload},
	}, "")

	res := fetchMiniMaxUsage(t.Context(), "key", "minimax")
	if res.message != "" {
		t.Fatalf("unexpected message: %q", res.message)
	}
	if _, ok := res.quotas["M-series (5h)"]; !ok {
		t.Errorf("expected the second endpoint to answer, got %v", res.quotas)
	}
}

// The same key rejection must NOT walk the fallback list: two extra requests
// against a live host for a key that can never work.
func TestFetchMiniMaxUsage_AuthFailureStopsFallback(t *testing.T) {
	srvCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srvCalls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	withMiniMaxUsageURLs(t, "minimax", []string{
		srv.URL + "/first", srv.URL + "/second",
	})

	res := fetchMiniMaxUsage(t.Context(), "key", "minimax")
	if !strings.Contains(res.message, "API key invalid or inactive") {
		t.Errorf("message = %q, want the invalid-key message", res.message)
	}
	if srvCalls != 1 {
		t.Errorf("provider was called %d times, want 1 (no fallback after an auth failure)", srvCalls)
	}
}

func TestFetchMiniMaxUsage_RequiresKey(t *testing.T) {
	res := fetchMiniMaxUsage(t.Context(), "  ", "minimax")
	if !strings.Contains(res.message, "API key not available") {
		t.Errorf("message = %q, want it to name the missing key", res.message)
	}
}

// camelCase payloads are accepted because MiniMax renamed its fields; dropping
// them would silently drop every row.
func TestFetchMiniMaxUsage_AcceptsCamelCaseFields(t *testing.T) {
	withMiniMaxServer(t, miniMaxURLs{paths: []string{"/token_plan/remains"}}, `{
	  "baseResp": {"statusCode": 0, "statusMsg": "success"},
	  "modelRemains": [{
	    "modelName": "general",
	    "currentIntervalTotalCount": 100,
	    "currentIntervalUsageCount": 10,
	    "remainsTime": 600000
	  }]
	}`)

	res := fetchMiniMaxUsage(t.Context(), "key", "minimax")
	row := miniMaxRow(t, res, "M-series (5h)")
	miniMaxAssertNum(t, row, "used", 10)
}

func TestMiniMaxQuotaName(t *testing.T) {
	tests := []struct {
		modelName string
		want      string
	}{
		{modelName: "MiniMax-M*", want: "M-series"},
		{modelName: "general", want: "M-series"},
		{modelName: "Video", want: "Video"},
		{modelName: "speech-02-hd", want: "Speech 02 HD"},
		{modelName: "english_expressive_voice", want: "English Expressive Voice"},
		{modelName: "", want: "MiniMax"},
	}

	for _, tt := range tests {
		t.Run(tt.modelName, func(t *testing.T) {
			got := miniMaxQuotaName(map[string]any{"model_name": tt.modelName})
			if got != tt.want {
				t.Errorf("miniMaxQuotaName(%q) = %q, want %q", tt.modelName, got, tt.want)
			}
		})
	}
}

// The dispatch must route minimax to the fetcher; before this the switch had no
// minimax case and every account fell through to the empty lock fallback.
func TestFetchProviderUsage_DispatchesMiniMax(t *testing.T) {
	withMiniMaxServer(t, miniMaxURLs{paths: []string{"/token_plan/remains"}}, miniMaxCountPayload)

	for _, provider := range []string{"minimax", "minimax-cn"} {
		t.Run(provider, func(t *testing.T) {
			res, ok := fetchProviderUsage(t.Context(), provider, map[string]any{"apiKey": "key"}, false)
			if !ok {
				t.Fatal("expected fetchProviderUsage to handle " + provider)
			}
			if len(res.quotas) == 0 {
				t.Errorf("expected quota rows, got %v (message %q)", res.quotas, res.message)
			}
		})
	}
}

// End-to-end through the dashboard route: the response must carry real counts,
// which is exactly what the tracker needs to draw a filled bar.
func TestHandleGetConnectionUsage_MiniMaxReturnsRealQuota(t *testing.T) {
	fastQuotaGate(t)
	withMiniMaxServer(t, miniMaxURLs{paths: []string{"/token_plan/remains"}}, miniMaxCountPayload)

	repo, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	if err := repo.CreateProviderConnectionFull(
		"mm-1", "minimax", "apikey", "mm-1", nil, `{"apiKey":"key"}`,
	); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	rec := httptest.NewRecorder()
	setupTestRouter(repo).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/usage/mm-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	body := miniMaxDecode(t, rec.Body.Bytes())
	quotas, ok := body["quotas"].(map[string]any)
	if !ok {
		t.Fatalf("expected a quotas object, got %v", body)
	}
	row, ok := quotas["M-series (5h)"].(map[string]any)
	if !ok {
		t.Fatalf("expected an M-series (5h) row, got %v", quotas)
	}
	miniMaxAssertNum(t, row, "used", 29)
	miniMaxAssertNum(t, row, "total", 100)
}

// A provider with no usage handler used to answer with rows synthesized from
// the connection's modelLock_ fields. They had no counts, so the dashboard drew
// them as "0 / ∞" at 0% — a fake exhausted quota, and a false positive for the
// "Turn off Empty" bulk action.
func TestHandleGetConnectionUsage_NoHandlerReportsNoUsageAPI(t *testing.T) {
	fastQuotaGate(t)

	repo, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	if err := repo.CreateProviderConnectionFull(
		"locked-1", "some-provider", "apikey", "locked-1", nil,
		`{"modelLock_minimax-image-01":"2030-01-01T00:00:00Z","rateLimitedUntil":"2030-01-01T00:00:00Z"}`,
	); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	rec := httptest.NewRecorder()
	setupTestRouter(repo).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/usage/locked-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	body := miniMaxDecode(t, rec.Body.Bytes())
	if quotas, present := body["quotas"]; present {
		if rows, ok := quotas.(map[string]any); !ok || len(rows) > 0 {
			t.Errorf("expected no synthesized quota rows, got %v", quotas)
		}
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "not implemented") {
		t.Errorf("message = %q, want it to say usage is not implemented", msg)
	}
}

// ---------- helpers ----------

type miniMaxURLs struct {
	paths    []string
	statuses []int
	bodies   []string
}

// withMiniMaxServer stands in for the MiniMax quota endpoints. One entry per
// requested path: statuses/bodies let a test script a per-URL answer, and body
// serves every path when the list is not being used to script them.
func withMiniMaxServer(t *testing.T, cfg miniMaxURLs, body string) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer key")
		}

		idx := 0
		for i, p := range cfg.paths {
			if strings.HasSuffix(r.URL.Path, p) {
				idx = i
				break
			}
		}
		status := http.StatusOK
		if idx < len(cfg.statuses) && cfg.statuses[idx] != 0 {
			status = cfg.statuses[idx]
		}
		payload := body
		if idx < len(cfg.bodies) {
			payload = cfg.bodies[idx]
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)

	paths := cfg.paths
	if len(paths) == 0 {
		paths = []string{"/token_plan/remains"}
	}
	urls := make([]string, 0, len(paths))
	for _, p := range paths {
		urls = append(urls, srv.URL+p)
	}
	withMiniMaxUsageURLs(t, "minimax", urls)
	withMiniMaxUsageURLs(t, "minimax-cn", urls)
}

func withMiniMaxUsageURLs(t *testing.T, provider string, urls []string) {
	t.Helper()
	prev := miniMaxUsageURLs[provider]
	miniMaxUsageURLs[provider] = urls
	t.Cleanup(func() { miniMaxUsageURLs[provider] = prev })
}

func miniMaxRow(t *testing.T, res usageResult, key string) map[string]any {
	t.Helper()
	row, ok := res.quotas[key].(map[string]any)
	if !ok {
		t.Fatalf("expected a %q row, got %v", key, res.quotas)
	}
	return row
}

func miniMaxAssertNum(t *testing.T, row map[string]any, key string, want float64) {
	t.Helper()
	got, ok := row[key].(float64)
	if !ok {
		t.Fatalf("expected numeric %q, got %v (%T)", key, row[key], row[key])
	}
	if math.Abs(got-want) > 0.001 {
		t.Errorf("%s = %v, want %v", key, got, want)
	}
}

func miniMaxRowResetAt(row map[string]any) string {
	s, _ := row["resetAt"].(string)
	return s
}

func miniMaxDecode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}
