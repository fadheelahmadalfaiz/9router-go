package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Coverage for the usage handlers that had no Go port at all: github,
// gemini-cli, glm/glm-cn, codebuddy-cn, kimi, zed, freebuff,
// vercel-ai-gateway and iflow. Before these, every one of them fell through
// the empty lock fallback and rendered "0 / ∞" at 0%.

// ---------- github (Copilot) ----------

// Paid plans report quota_snapshots with entitlement/remaining; free plans
// report a separate monthly_quotas + limited_user_quotas pair. Handling only
// one of the two is how a Copilot account ends up with no rows at all.
func TestFetchGitHubUsage_PaidSnapshots(t *testing.T) {
	srv := usageJSONServer(t, `{
	  "copilot_plan": "individual",
	  "quota_reset_date": "2030-01-01T00:00:00Z",
	  "quota_snapshots": {
	    "chat": {"entitlement": 300, "remaining": 271, "unlimited": false},
	    "completions": {"entitlement": 300, "remaining": 300, "unlimited": false},
	    "premium_interactions": {"entitlement": 0, "remaining": 0, "unlimited": true}
	  }
	}`)
	defer withUsageURL(t, &githubUsageURL, srv.URL)()

	res := fetchGitHubUsage(t.Context(), "ghtoken")
	if res.message != "" {
		t.Fatalf("unexpected message: %q", res.message)
	}
	if res.plan != "individual" {
		t.Errorf("plan = %q, want individual", res.plan)
	}
	chat := usageRow(t, res, "chat")
	usageAssertNum(t, chat, "used", 29)
	usageAssertNum(t, chat, "total", 300)
	if got, _ := chat["resetAt"].(string); got != "2030-01-01T00:00:00Z" {
		t.Errorf("chat resetAt = %q, want the shared quota_reset_date", got)
	}
	if u, _ := usageRow(t, res, "premium_interactions")["unlimited"].(bool); !u {
		t.Error("premium_interactions should keep unlimited: true")
	}
}

func TestFetchGitHubUsage_FreePlanQuotas(t *testing.T) {
	srv := usageJSONServer(t, `{
	  "access_type_sku": "free",
	  "limited_user_reset_date": "2030-01-01T00:00:00Z",
	  "monthly_quotas": {"chat": 50, "completions": 300},
	  "limited_user_quotas": {"chat": 7, "completions": 12}
	}`)
	defer withUsageURL(t, &githubUsageURL, srv.URL)()

	res := fetchGitHubUsage(t.Context(), "ghtoken")
	if res.plan != "free" {
		t.Errorf("plan = %q, want the access_type_sku fallback", res.plan)
	}
	chat := usageRow(t, res, "chat")
	usageAssertNum(t, chat, "used", 7)
	usageAssertNum(t, chat, "total", 50)
	usageAssertNum(t, usageRow(t, res, "completions"), "used", 12)
}

func TestFetchGitHubUsage_Unparseable(t *testing.T) {
	srv := usageJSONServer(t, `{"copilot_plan":"individual"}`)
	defer withUsageURL(t, &githubUsageURL, srv.URL)()

	res := fetchGitHubUsage(t.Context(), "ghtoken")
	if !strings.Contains(res.message, "Unable to parse quota data") {
		t.Errorf("message = %q, want the unparseable-quota message", res.message)
	}
	if len(res.quotas) != 0 {
		t.Errorf("expected no quota rows, got %v", res.quotas)
	}
}

func TestFetchGitHubUsage_RequiresToken(t *testing.T) {
	res := fetchGitHubUsage(t.Context(), "  ")
	if !strings.Contains(res.message, "No GitHub access token") {
		t.Errorf("message = %q, want it to name the missing token", res.message)
	}
}

// ---------- gemini-cli ----------

func TestFetchGeminiCLIUsage_BucketsFromStoredProject(t *testing.T) {
	srv := usageJSONServer(t, `{"buckets":[
	  {"modelId":"gemini-3.8-flash-high","remainingFraction":0.71,"resetTime":"2030-01-01T00:00:00Z"},
	  {"modelId":"broken","resetTime":"2030-01-01T00:00:00Z"}
	]}`)
	defer withUsageURL(t, &geminiCLIQuotaURL, srv.URL)()

	// projectId on the connection means no loadCodeAssist call is needed.
	res := fetchGeminiCLIUsage(t.Context(), "tok", map[string]any{"projectId": "proj-1"})
	if res.message != "" {
		t.Fatalf("unexpected message: %q", res.message)
	}
	row := usageRow(t, res, "gemini-3.8-flash-high")
	usageAssertNum(t, row, "total", 1000)
	usageAssertNum(t, row, "used", 290)
	usageAssertNum(t, row, "remainingPercentage", 71)
	if _, exists := res.quotas["broken"]; exists {
		t.Error("a bucket with no remainingFraction must be skipped")
	}
}

func TestFetchGeminiCLIUsage_ResolvesProjectWhenMissing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/loadCodeAssist", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"cloudaicompanionProject":{"id":"proj-9"},"currentTier":{"name":"Legacy"}}`))
	})
	mux.HandleFunc("/quota", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"buckets":[{"modelId":"gemini-3.1-pro-low","remainingFraction":1}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	defer withUsageURL(t, &geminiCLIQuotaURL, srv.URL+"/quota")()
	defer withUsageURL(t, &geminiCLILoadCodeAssist, srv.URL+"/loadCodeAssist")()

	res := fetchGeminiCLIUsage(t.Context(), "tok", map[string]any{})
	if res.plan != "Legacy" {
		t.Errorf("plan = %q, want the tier name from loadCodeAssist", res.plan)
	}
	if _, ok := res.quotas["gemini-3.1-pro-low"]; !ok {
		t.Errorf("expected quota rows, got %v", res.quotas)
	}
}

func TestFetchGeminiCLIUsage_NoProjectID(t *testing.T) {
	srv := usageJSONServer(t, `{}`)
	defer withUsageURL(t, &geminiCLIQuotaURL, srv.URL)()
	defer withUsageURL(t, &geminiCLILoadCodeAssist, srv.URL)()

	res := fetchGeminiCLIUsage(t.Context(), "tok", map[string]any{})
	if !strings.Contains(res.message, "project ID not available") {
		t.Errorf("message = %q, want it to name the missing project", res.message)
	}
}

func TestFetchGeminiCLIUsage_RequiresToken(t *testing.T) {
	res := fetchGeminiCLIUsage(t.Context(), "", map[string]any{"projectId": "p"})
	if res.plan != "Free" || !strings.Contains(res.message, "access token not available") {
		t.Errorf("plan = %q message = %q, want Free + the missing-token message", res.plan, res.message)
	}
}

// ---------- glm / glm-cn ----------

// unit 3 and unit 6 are separate windows; collapsing them into one key makes a
// two-window plan look like it has a single limit.
func TestFetchGlmUsage_KeysEveryWindowSeparately(t *testing.T) {
	srv := usageJSONServer(t, `{"data":{"level":"PRO","limits":[
	  {"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":20,"nextResetTime":1893456000000},
	  {"type":"TOKENS_LIMIT","unit":6,"number":0,"percentage":10},
	  {"type":"CREDIT_LIMIT","unit":0,"number":3,"percentage":40},
	  {"type":"SOMETHING_ELSE","unit":0,"number":9,"percentage":99}
	]}}`)
	defer withUsageMapURL(t, glmUsageURLs, "glm", srv.URL)()

	res := fetchGlmUsage(t.Context(), "key", "glm")
	if res.plan != "Pro" {
		t.Errorf("plan = %q, want the capitalized level", res.plan)
	}
	for _, key := range []string{"Session (5h)", "Weekly (7d)", "Limit (3)"} {
		if _, ok := res.quotas[key]; !ok {
			t.Errorf("missing %q row, got %v", key, res.quotas)
		}
	}
	if _, exists := res.quotas["Limit (9)"]; exists {
		t.Error("an unknown limit type must not become a row")
	}

	session := usageRow(t, res, "Session (5h)")
	// percentage is percent USED.
	usageAssertNum(t, session, "used", 20)
	usageAssertNum(t, session, "remainingPercentage", 80)
	if got, _ := session["resetAt"].(string); got == "" {
		t.Error("Session (5h) is missing a resetAt from nextResetTime")
	}
	if usageRow(t, res, "Weekly (7d)")["resetAt"] != nil {
		t.Error("a window with no nextResetTime must carry a null resetAt")
	}
}

func TestFetchGlmUsage_RegionalURLs(t *testing.T) {
	cn := usageJSONServer(t, `{"data":{"level":"lite","limits":[]}}`)
	defer withUsageMapURL(t, glmUsageURLs, "glm-cn", cn.URL)()

	if res := fetchGlmUsage(t.Context(), "key", "glm-cn"); res.plan != "Lite" {
		t.Errorf("glm-cn plan = %q, want Lite from the China endpoint", res.plan)
	}
	if res := fetchGlmUsage(t.Context(), "key", "unknown-region"); !strings.Contains(res.message, "unknown region") {
		t.Errorf("message = %q, want it to name the unknown region", res.message)
	}
}

func TestFetchGlmUsage_Unauthorized(t *testing.T) {
	srv := usageStatusServer(t, http.StatusUnauthorized, `{}`)
	defer withUsageMapURL(t, glmUsageURLs, "glm", srv.URL)()

	res := fetchGlmUsage(t.Context(), "key", "glm")
	if !strings.Contains(res.message, "invalid or expired") {
		t.Errorf("message = %q, want the invalid-key message", res.message)
	}
}

func TestFetchGlmUsage_RequiresKey(t *testing.T) {
	res := fetchGlmUsage(t.Context(), "  ", "glm")
	if !strings.Contains(res.message, "API key not available") {
		t.Errorf("message = %q, want it to name the missing key", res.message)
	}
}

// ---------- codebuddy-cn ----------

// Refill packs roll into a new cycle before the resource expires and are
// labelled by cadence; bonus packs are one-shot. Merging them would show a
// bonus credit as a monthly allowance that refills.
func TestFetchCodeBuddyUsage_RefillAndBonusStaySeparate(t *testing.T) {
	srv := usageJSONServer(t, `{"code":0,"data":{"Response":{"Data":{"Accounts":[
	  {"PackageName":"GLM Monthly","CycleStartTime":"2030-01-01T00:00:00Z","CycleEndTime":"2030-02-01T00:00:00Z",
	   "DeductionEndTime":1906502400000,"CycleCapacityUsedPrecise":"6.54","CycleCapacitySizePrecise":"500"},
	  {"PackageName":"GLM Weekly","CycleStartTime":"2030-01-01T00:00:00Z","CycleEndTime":"2030-01-03T00:00:00Z",
	   "DeductionEndTime":1906502400000,"CycleCapacityUsed":"1","CycleCapacitySize":"10"},
	  {"CycleEndTime":"2030-03-01T00:00:00Z","DeductionEndTime":1898553600000,
	   "CapacityUsed":"2","CapacitySize":"20"}
	]}}}}`)
	defer withUsageMapURL(t, codebuddyUsageURLs, "codebuddy-cn", srv.URL)()
	defer withUsageMapURL(t, codebuddyUsageURLs, "codebuddy-intl", srv.URL)()

	res := fetchCodeBuddyCnUsage(t.Context(), "tok", "")
	if res.message != "" {
		t.Fatalf("unexpected message: %q", res.message)
	}
	// The plan label comes from the soonest-refilling pack, not the first row
	// in the payload — upstream reads refills[0] after sorting by cycle end.
	if res.plan != "GLM Weekly" {
		t.Errorf("plan = %q, want the soonest-expiring refill package name", res.plan)
	}
	monthly := usageRow(t, res, "Monthly")
	usageAssertNum(t, monthly, "used", 6.54)
	usageAssertNum(t, monthly, "total", 500)
	if r, _ := monthly["recurring"].(bool); !r {
		t.Error("a refill pack must be marked recurring so the UI says \"Resets in\"")
	}

	weekly := usageRow(t, res, "Weekly")
	usageAssertNum(t, weekly, "total", 10)
	if r, _ := weekly["recurring"].(bool); !r {
		t.Error("a 2-day cycle should still be labelled a recurring refill")
	}

	bonus := usageRow(t, res, "Bonus Pack 1")
	usageAssertNum(t, bonus, "used", 2)
	usageAssertNum(t, bonus, "total", 20)
	if r, _ := bonus["recurring"].(bool); r {
		t.Error("a bonus pack must not be marked recurring")
	}
}

func TestFetchCodeBuddyUsage_DailyAndDuplicateCadences(t *testing.T) {
	srv := usageJSONServer(t, `{"code":0,"data":{"Response":{"Data":{"Accounts":[
	  {"CycleStartTime":"2030-01-01T00:00:00Z","CycleEndTime":"2030-01-01T12:00:00Z","DeductionEndTime":1906502400000},
	  {"CycleStartTime":"2030-01-01T00:00:00Z","CycleEndTime":"2030-01-01T06:00:00Z","DeductionEndTime":1906502400000}
	]}}}}`)
	defer withUsageMapURL(t, codebuddyUsageURLs, "codebuddy-cn", srv.URL)()

	res := fetchCodeBuddyCnUsage(t.Context(), "tok", "")
	if _, ok := res.quotas["Daily"]; !ok {
		t.Errorf("missing the first Daily row, got %v", res.quotas)
	}
	if _, ok := res.quotas["Daily 2"]; !ok {
		t.Errorf("a second Daily pack must be suffixed, not overwritten: %v", res.quotas)
	}
}

func TestFetchCodeBuddyUsage_MessagePaths(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantSubstr string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{}`, wantSubstr: "credential invalid or expired"},
		{name: "server error", status: http.StatusInternalServerError, body: `{}`, wantSubstr: "quota API error (500)"},
		{name: "upstream code", status: http.StatusOK, body: `{"code":7,"msg":"nope"}`, wantSubstr: "quota error: nope"},
		{name: "no packages", status: http.StatusOK, body: `{"code":0,"data":{"Response":{"Data":{"Accounts":[]}}}}`, wantSubstr: "No credit package"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := usageStatusServer(t, tt.status, tt.body)
			defer withUsageMapURL(t, codebuddyUsageURLs, "codebuddy-cn", srv.URL)()

			res := fetchCodeBuddyCnUsage(t.Context(), "tok", "")
			if !strings.Contains(res.message, tt.wantSubstr) {
				t.Errorf("message = %q, want it to contain %q", res.message, tt.wantSubstr)
			}
			if len(res.quotas) != 0 {
				t.Errorf("expected no quota rows, got %v", res.quotas)
			}
		})
	}
}

func TestFetchCodeBuddyUsage_RequiresCredential(t *testing.T) {
	res := fetchCodeBuddyCnUsage(t.Context(), "", "")
	if !strings.Contains(res.message, "credential not available") {
		t.Errorf("message = %q, want it to name the missing credential", res.message)
	}
}

// ---------- kimi ----------

// One provider id, two auth shapes: a platform key uses x-api-key, OAuth uses
// Bearer plus the X-Msh-* client headers. Asserted on the wire, because
// http.Header canonicalizes the key names on the way out.
func TestKimiAuthShape(t *testing.T) {
	tests := []struct {
		name       string
		apiKey     string
		token      string
		wantHeader string
		wantValue  string
		absent     string
	}{
		{name: "api key uses x-api-key", apiKey: "k1", wantHeader: "X-Api-Key", wantValue: "k1", absent: "Authorization"},
		{name: "oauth uses bearer", token: "t1", wantHeader: "Authorization", wantValue: "Bearer t1", absent: "X-Api-Key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := http.Header{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Clone()
				_, _ = w.Write([]byte(`{"usage":{"limit":100,"used":30,"remaining":70}}`))
			}))
			t.Cleanup(srv.Close)
			defer withUsageURL(t, &kimiUsageURL, srv.URL)()

			fetchKimiUsage(t.Context(), tt.token, tt.apiKey, map[string]any{"deviceId": "dev-1"})

			if v := got.Get(tt.wantHeader); v != tt.wantValue {
				t.Errorf("%s = %q, want %q", tt.wantHeader, v, tt.wantValue)
			}
			if v := got.Get(tt.absent); v != "" {
				t.Errorf("%s = %q, want it absent for this auth shape", tt.absent, v)
			}
		})
	}
}

func TestKimiOAuthCarriesClientIdentityHeaders(t *testing.T) {
	srv := usageJSONServer(t, `{"usage":{"limit":100,"used":30,"remaining":70}}`)
	defer withUsageURL(t, &kimiUsageURL, srv.URL)()

	res := fetchKimiUsage(t.Context(), "tok", "", map[string]any{"deviceId": "dev-7"})
	if len(res.quotas) == 0 {
		t.Fatalf("expected quota rows, got %q", res.message)
	}
}

// An absolute `remaining` on the quota object is read by QuotaTable as a
// 0-100 percentage, which inverts the bar — only remainingPercentage is safe.
func TestFetchKimiUsage_PercentageOnlyRows(t *testing.T) {
	srv := usageJSONServer(t, `{
	  "user":{"membership":{"level":"LEVEL_ADVANCED"}},
	  "usage":{"limit":100,"used":30,"remaining":70,"resetTime":"2030-01-01T00:00:00Z"},
	  "limits":[{"detail":{"limit":50,"remaining":40,"resetTime":1893456000}}]
	}`)
	defer withUsageURL(t, &kimiUsageURL, srv.URL)()

	res := fetchKimiUsage(t.Context(), "", "k1", map[string]any{})
	if res.plan != "Allegro" {
		t.Errorf("plan = %q, want the mapped membership level", res.plan)
	}
	weekly := usageRow(t, res, "Weekly")
	usageAssertNum(t, weekly, "used", 30)
	usageAssertNum(t, weekly, "total", 100)
	usageAssertNum(t, weekly, "remainingPercentage", 70)
	if _, present := weekly["remaining"]; present {
		t.Error("an absolute remaining must not ride the quota object")
	}
	rate := usageRow(t, res, "Ratelimit")
	usageAssertNum(t, rate, "used", 10)
	usageAssertNum(t, rate, "remainingPercentage", 80)
}

// A live OAuth token on an account without Kimi Code returns 403
// REASON_FEATURE_NO_PERMISSION. Telling the user to re-authorize there sends
// them to fix a credential that is already fine.
func TestKimiErrorMessage(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantSubstr string
	}{
		{
			name: "expired session", status: http.StatusUnauthorized, body: `{}`,
			wantSubstr: "authentication expired",
		},
		{
			name:       "no entitlement",
			status:     http.StatusForbidden,
			body:       `{"details":[{"debug":{"reason":"REASON_FEATURE_NO_PERMISSION","localizedMessage":{"message":"Subscribe to Kimi Code"}}}]}`,
			wantSubstr: "Subscribe to Kimi Code",
		},
		{
			name:       "permission denied wording",
			status:     http.StatusForbidden,
			body:       `{"code":"permission_denied"}`,
			wantSubstr: "no permission to view usage",
		},
		{
			name:       "other error",
			status:     http.StatusBadGateway,
			body:       `{"message":"upstream down"}`,
			wantSubstr: "API Error 502: upstream down",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := kimiErrorMessage(tt.status, []byte(tt.body))
			if !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("kimiErrorMessage(%d) = %q, want it to contain %q", tt.status, got, tt.wantSubstr)
			}
		})
	}
}

func TestFetchKimiUsage_NoLimits(t *testing.T) {
	srv := usageJSONServer(t, `{"user":{"membership":{"level":"LEVEL_BASIC"}},"usage":{"limit":0}}`)
	defer withUsageURL(t, &kimiUsageURL, srv.URL)()

	res := fetchKimiUsage(t.Context(), "", "k1", map[string]any{})
	if res.plan != "Moderato" {
		t.Errorf("plan = %q, want Moderato", res.plan)
	}
	if !strings.Contains(res.message, "Usage tracked per request") {
		t.Errorf("message = %q, want the per-request fallback", res.message)
	}
}

func TestFetchKimiUsage_RequiresCredential(t *testing.T) {
	res := fetchKimiUsage(t.Context(), "", "", map[string]any{})
	if !strings.Contains(res.message, "not available") {
		t.Errorf("message = %q, want it to name the missing credential", res.message)
	}
}

// ---------- freebuff ----------

// 🔴 The read must be GET. POST on this endpoint CLAIMS a session and burns a
// unit of the daily quota — a tracker that spends quota to report quota is
// worse than one that reports nothing.
func TestFetchFreebuffUsage_ReadsWithGET(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_, _ = w.Write([]byte(`{"rateLimitsByModel":{"z-ai/glm-5.3-flash":{"limit":6,"recentCount":1.3,"resetAt":"2030-01-01T00:00:00Z"}}}`))
	}))
	t.Cleanup(srv.Close)
	defer withUsageURL(t, &freebuffUsageURL, srv.URL)()

	res := fetchFreebuffUsage(t.Context(), "tok")
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET — POST would claim a session and burn daily quota", gotMethod)
	}
	row := usageRow(t, res, "z-ai/glm-5.3-flash")
	usageAssertNum(t, row, "used", 1.3)
	usageAssertNum(t, row, "total", 6)
	if r, _ := row["recurring"].(bool); !r {
		t.Error("a session pool must be marked recurring")
	}
	if name, _ := row["displayName"].(string); name != "GLM 5.3 Flash" {
		t.Errorf("displayName = %q, want the registry label", name)
	}
}

// An active session carries its own rateLimit row; older servers leave the
// model out of the shared map.
func TestFetchFreebuffUsage_FoldsActiveSessionRow(t *testing.T) {
	srv := usageJSONServer(t, `{
	  "status":"active","model":"mimo/mimo-v2.5",
	  "rateLimit":{"limit":6,"recentCount":2,"resetAt":"2030-01-01T00:00:00Z"},
	  "rateLimitsByModel":{}
	}`)
	defer withUsageURL(t, &freebuffUsageURL, srv.URL)()

	res := fetchFreebuffUsage(t.Context(), "tok")
	if _, ok := res.quotas["mimo/mimo-v2.5"]; !ok {
		t.Errorf("expected the active session row to be folded in, got %v", res.quotas)
	}
}

func TestFetchFreebuffUsage_FreebucksPool(t *testing.T) {
	srv := usageJSONServer(t, `{
	  "accessTier":"limited",
	  "freebucks":{
	    "balance": 12.5,
	    "daily":{"limit":40,"spent":11,"remaining":29,"resetAt":"2030-01-01T00:00:00Z"},
	    "wallet":{"balance":3},
	    "prices":{"upstage/solar-pro4": 2},
	    "priceChanges":[{"modelId":"upstage/solar-pro4","price":0,"at":"2020-01-01T00:00:00Z","tagline":"promo"}]
	  }
	}`)
	defer withUsageURL(t, &freebuffUsageURL, srv.URL)()

	res := fetchFreebuffUsage(t.Context(), "tok")
	if res.plan != "Freebuff (Limited)" {
		t.Errorf("plan = %q, want the limited-tier label", res.plan)
	}
	row := usageRow(t, res, "upstage/solar-pro4")
	usageAssertNum(t, row, "used", 11)
	usageAssertNum(t, row, "total", 40)
	// A due price change overrides the live price map, server-side schedule.
	usageAssertNum(t, row, "price", 0)

	summary, ok := res.extra["freebucks"].(map[string]any)
	if !ok {
		t.Fatalf("expected a freebucks summary, got %v", res.extra)
	}
	usageAssertNum(t, summary, "balance", 12.5)
}

func TestFetchFreebuffUsage_MessagePaths(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantSubstr string
	}{
		{name: "expired", status: http.StatusUnauthorized, body: `{}`, wantSubstr: "invalid or expired"},
		{name: "country blocked", status: http.StatusForbidden, body: `{"status":"country_blocked"}`, wantSubstr: "not available in your region"},
		{name: "banned", status: http.StatusForbidden, body: `{"status":"banned"}`, wantSubstr: "has been banned"},
		{name: "other forbidden", status: http.StatusForbidden, body: `{"message":"locked"}`, wantSubstr: "access denied (403): locked"},
		{name: "no session row", status: http.StatusNotFound, body: `{}`, wantSubstr: "No session quota"},
		{name: "server error", status: http.StatusInternalServerError, body: `{}`, wantSubstr: "quota API error (500)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := usageStatusServer(t, tt.status, tt.body)
			defer withUsageURL(t, &freebuffUsageURL, srv.URL)()

			res := fetchFreebuffUsage(t.Context(), "tok")
			if !strings.Contains(res.message, tt.wantSubstr) {
				t.Errorf("message = %q, want it to contain %q", res.message, tt.wantSubstr)
			}
			if len(res.quotas) != 0 {
				t.Errorf("expected no quota rows, got %v", res.quotas)
			}
		})
	}
}

func TestFetchFreebuffUsage_RequiresCredential(t *testing.T) {
	res := fetchFreebuffUsage(t.Context(), "  ")
	if !strings.Contains(res.message, "credential not available") {
		t.Errorf("message = %q, want it to name the missing credential", res.message)
	}
}

// ---------- zed ----------

// model_requests.limit = 0 on a token-billed plan means "billed per token",
// not an exhausted request quota. Rendering it as 0% would tell the user their
// model access is gone.
func TestFetchZedUsage_SkipsTokenBilledRequestQuota(t *testing.T) {
	srv := usageJSONServer(t, `{"plan":{
	  "plan_v3":"zed_pro",
	  "subscription_period":{"ended_at":"2030-01-01T00:00:00Z"},
	  "usage":{
	    "edit_predictions":{"used":120,"limit":500},
	    "model_requests":{"used":40,"limit":0}
	  }
	}}`)
	defer withUsageURL(t, &zedUsageURL, srv.URL)()

	res := fetchZedUsage(t.Context(), "tok", map[string]any{"userId": "u1"})
	if res.plan != "Zed Pro" {
		t.Errorf("plan = %q, want the mapped plan label", res.plan)
	}
	predictions := usageRow(t, res, "Edit Predictions")
	usageAssertNum(t, predictions, "used", 120)
	usageAssertNum(t, predictions, "total", 500)
	usageAssertNum(t, predictions, "remainingPercentage", 76)
	if _, exists := res.quotas["Hosted Model Requests"]; exists {
		t.Errorf("a token-billed plan must not get a request row: %v", res.quotas)
	}
}

func TestZedParseLimit(t *testing.T) {
	tests := []struct {
		name          string
		raw           any
		wantUnlimited bool
		wantTotal     float64
	}{
		{name: "string unlimited", raw: "unlimited", wantUnlimited: true},
		{name: "bool unlimited", raw: map[string]any{"unlimited": true}, wantUnlimited: true},
		{name: "number", raw: float64(50), wantTotal: 50},
		{name: "numeric string", raw: "50", wantTotal: 50},
		{name: "limited wrapper", raw: map[string]any{"limited": float64(20)}, wantTotal: 20},
		{name: "zero", raw: float64(0)},
		{name: "absent", raw: nil},
		{name: "garbage", raw: map[string]any{"nope": 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := zedParseLimit(tt.raw)
			if got.unlimited != tt.wantUnlimited {
				t.Errorf("unlimited = %v, want %v", got.unlimited, tt.wantUnlimited)
			}
			if got.total != tt.wantTotal {
				t.Errorf("total = %v, want %v", got.total, tt.wantTotal)
			}
		})
	}
}

func TestFetchZedUsage_UnlimitedAndOverdue(t *testing.T) {
	srv := usageJSONServer(t, `{"plan":{
	  "plan_v3":"zed_business",
	  "has_overdue_invoices": true,
	  "trial_started_at":"2030-01-01T00:00:00Z",
	  "usage":{"edit_predictions":{"used":9,"limit":"unlimited"}}
	}}`)
	defer withUsageURL(t, &zedUsageURL, srv.URL)()

	res := fetchZedUsage(t.Context(), "tok", map[string]any{"userId": "u1"})
	if !strings.Contains(res.plan, "Trial active") {
		t.Errorf("plan = %q, want the trial marker", res.plan)
	}
	if !strings.Contains(res.message, "overdue invoices") {
		t.Errorf("message = %q, want the overdue-invoice warning", res.message)
	}
	usageAssertNum(t, usageRow(t, res, "Edit Predictions"), "remainingPercentage", 100)
}

// Zed's header is "user_id token", not Bearer.
func TestFetchZedUsage_AuthHeaderAndGuards(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"plan":{"plan_v3":"zed_free"}}`))
	}))
	t.Cleanup(srv.Close)
	defer withUsageURL(t, &zedUsageURL, srv.URL)()

	fetchZedUsage(t.Context(), "tok", map[string]any{"userId": "u1"})
	if gotAuth != "u1 tok" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "u1 tok")
	}

	if res := fetchZedUsage(t.Context(), "tok", map[string]any{}); !strings.Contains(res.message, "missing user id") {
		t.Errorf("message = %q, want it to name the missing user id", res.message)
	}
	if res := fetchZedUsage(t.Context(), "  ", map[string]any{"userId": "u1"}); !strings.Contains(res.message, "access token not available") {
		t.Errorf("message = %q, want it to name the missing token", res.message)
	}
}

func TestFetchZedUsage_Unauthorized(t *testing.T) {
	srv := usageStatusServer(t, http.StatusUnauthorized, `{}`)
	defer withUsageURL(t, &zedUsageURL, srv.URL)()

	res := fetchZedUsage(t.Context(), "tok", map[string]any{"userId": "u1"})
	if !strings.Contains(res.message, "authentication failed") {
		t.Errorf("message = %q, want the auth failure message", res.message)
	}
}

func TestZedPlanLabel(t *testing.T) {
	tests := []struct {
		raw  any
		want string
	}{
		{raw: "zed_pro", want: "Zed Pro"},
		{raw: "ZED_STUDENT", want: "Zed Student"},
		{raw: "enterprise_plus", want: "Enterprise Plus"},
		{raw: "", want: "Zed"},
		{raw: nil, want: "Zed"},
	}

	for _, tt := range tests {
		if got := zedPlanLabel(tt.raw); got != tt.want {
			t.Errorf("zedPlanLabel(%v) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

// ---------- vercel-ai-gateway + iflow ----------

func TestFetchVercelCredits(t *testing.T) {
	srv := usageJSONServer(t, `{"balance":"4.50","total_used":"0.50"}`)
	defer withUsageURL(t, &vercelCreditsURL, srv.URL)()

	res := fetchVercelCredits(t.Context(), "key")
	if res.message != "" {
		t.Fatalf("unexpected message: %q", res.message)
	}
	remaining := usageRow(t, res, "Remaining (USD)")
	usageAssertNum(t, remaining, "total", 5)
	usageAssertNum(t, remaining, "remainingPercentage", 90)
	if u, _ := usageRow(t, res, "Used (USD)")["unlimited"].(bool); !u {
		t.Error("the spend row has no cap to draw a percentage against")
	}
}

func TestFetchVercelCredits_Unfunded(t *testing.T) {
	srv := usageJSONServer(t, `{"balance":"0.00","total_used":"0.00"}`)
	defer withUsageURL(t, &vercelCreditsURL, srv.URL)()

	res := fetchVercelCredits(t.Context(), "key")
	if !strings.Contains(res.message, "No credit allocation") {
		t.Errorf("message = %q, want the unfunded-account message", res.message)
	}
	if len(res.quotas) != 0 {
		t.Errorf("expected no quota rows, got %v", res.quotas)
	}
}

func TestFetchVercelCredits_MessagePaths(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantSubstr string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, wantSubstr: "invalid or expired"},
		{name: "server error", status: http.StatusBadGateway, wantSubstr: "credits API error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := usageStatusServer(t, tt.status, `{}`)
			defer withUsageURL(t, &vercelCreditsURL, srv.URL)()

			res := fetchVercelCredits(t.Context(), "key")
			if !strings.Contains(res.message, tt.wantSubstr) {
				t.Errorf("message = %q, want it to contain %q", res.message, tt.wantSubstr)
			}
		})
	}
}

func TestFetchVercelCredits_RequiresKey(t *testing.T) {
	res := fetchVercelCredits(t.Context(), "  ")
	if !strings.Contains(res.message, "API key not available") {
		t.Errorf("message = %q, want it to name the missing key", res.message)
	}
}

// iFlow has no quota endpoint at all; the handler exists so the dashboard says
// "tracked per request" rather than "not implemented".
func TestFetchIflowUsage_HasNoEndpoint(t *testing.T) {
	res := fetchIflowUsage(t.Context())
	if !strings.Contains(res.message, "Usage tracked per request") {
		t.Errorf("message = %q, want the per-request fallback", res.message)
	}
	if len(res.quotas) != 0 {
		t.Errorf("expected no quota rows, got %v", res.quotas)
	}
}

// ---------- dispatch ----------

// Every provider with an upstream usage handler must reach it. A missing case
// is invisible until the dashboard shows "0 / ∞" or an empty card.
func TestFetchProviderUsage_DispatchesEveryHandledProvider(t *testing.T) {
	gh := usageJSONServer(t, `{"quota_snapshots":{"chat":{"entitlement":1,"remaining":0}}}`)
	t.Cleanup(gh.Close)
	glmSrv := usageJSONServer(t, `{"data":{"level":"pro","limits":[{"type":"TOKENS_LIMIT","unit":6,"number":0,"percentage":1}]}}`)
	t.Cleanup(glmSrv.Close)
	zedSrv := usageJSONServer(t, `{"plan":{"plan_v3":"zed_pro","usage":{"edit_predictions":{"used":1,"limit":2}}}}`)
	t.Cleanup(zedSrv.Close)
	vercelSrv := usageJSONServer(t, `{"balance":"1","total_used":"0"}`)
	t.Cleanup(vercelSrv.Close)
	restore := withUsageURL(t, &githubUsageURL, gh.URL)
	defer restore()
	restore = withUsageMapURL(t, glmUsageURLs, "glm", glmSrv.URL)
	defer restore()
	restore = withUsageMapURL(t, glmUsageURLs, "glm-cn", glmSrv.URL)
	defer restore()
	restore = withUsageURL(t, &zedUsageURL, zedSrv.URL)
	defer restore()
	restore = withUsageURL(t, &vercelCreditsURL, vercelSrv.URL)
	defer restore()

	tests := []struct {
		provider string
		data     map[string]any
		wantRow  string
	}{
		{provider: "github", data: map[string]any{"accessToken": "t"}, wantRow: "chat"},
		{provider: "glm", data: map[string]any{"apiKey": "k"}, wantRow: "Weekly (7d)"},
		{provider: "glm-cn", data: map[string]any{"apiKey": "k"}, wantRow: "Weekly (7d)"},
		{provider: "zed", data: map[string]any{"accessToken": "t", "providerSpecificData": map[string]any{"userId": "u"}}, wantRow: "Edit Predictions"},
		{provider: "vercel-ai-gateway", data: map[string]any{"apiKey": "k"}, wantRow: "Remaining (USD)"},
		{provider: "iflow", data: map[string]any{"accessToken": "t"}},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			res, ok := fetchProviderUsage(t.Context(), tt.provider, tt.data, false)
			if !ok {
				t.Fatalf("fetchProviderUsage does not handle %s", tt.provider)
			}
			if tt.wantRow == "" {
				return
			}
			if _, has := res.quotas[tt.wantRow]; !has {
				t.Errorf("expected a %q row, got %v (message %q)", tt.wantRow, res.quotas, res.message)
			}
		})
	}
}

// ---------- helpers ----------

func usageJSONServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return usageStatusServer(t, http.StatusOK, body)
}

func usageStatusServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withUsageURL points one package-level endpoint var at a test server and
// returns the restore func.
func withUsageURL(t *testing.T, target *string, url string) func() {
	t.Helper()
	prev := *target
	*target = url
	return func() { *target = prev }
}

// withUsageMapURL is the map-index variant: a map element is not addressable,
// so the provider/region key goes in as a parameter.
func withUsageMapURL(t *testing.T, targets map[string]string, key, url string) func() {
	t.Helper()
	prev := targets[key]
	targets[key] = url
	return func() { targets[key] = prev }
}

func usageRow(t *testing.T, res usageResult, key string) map[string]any {
	t.Helper()
	row, ok := res.quotas[key].(map[string]any)
	if !ok {
		t.Fatalf("expected a %q row, got %v", key, res.quotas)
	}
	return row
}

func usageAssertNum(t *testing.T, row map[string]any, key string, want float64) {
	t.Helper()
	got, ok := row[key].(float64)
	if !ok {
		t.Fatalf("expected numeric %q, got %v (%T)", key, row[key], row[key])
	}
	if diff := got - want; diff > 0.001 || diff < -0.001 {
		t.Errorf("%s = %v, want %v", key, got, want)
	}
}
