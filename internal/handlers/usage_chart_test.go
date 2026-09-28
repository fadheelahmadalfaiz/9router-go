package handlers

import (
	"9router/proxy/internal/db"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func seedDailyUsage(t *testing.T, repo *db.Repo, dateKey string, requests, prompt, completion int, cost float64) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"requests":         requests,
		"promptTokens":     prompt,
		"completionTokens": completion,
		"cost":             cost,
	})
	if err != nil {
		t.Fatalf("marshal daily payload: %v", err)
	}
	if err := repo.UpsertUsageDaily(dateKey, string(payload)); err != nil {
		t.Fatalf("seed usageDaily %s: %v", dateKey, err)
	}
}

func getChart(t *testing.T, repo *db.Repo, query string) ([]UsageChartPoint, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/usage/chart"+query, nil)
	HandleUsageChart(repo)(rec, req)

	if rec.Code != http.StatusOK {
		return nil, rec.Code
	}
	var points []UsageChartPoint
	if err := json.Unmarshal(rec.Body.Bytes(), &points); err != nil {
		t.Fatalf("decode chart payload: %v (body %s)", err, rec.Body.String())
	}
	return points, rec.Code
}

// TestHandleUsageChart_PeriodBuckets — each period returns the bucket count and
// granularity upstream getChartData promises: 24 hourly buckets for today/24h,
// one bucket per day for the rolling periods, and every day on record for "all".
func TestHandleUsageChart_PeriodBuckets(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	// Two days on record plus today.
	now := time.Now().UTC()
	seedDailyUsage(t, repo, now.AddDate(0, 0, -1).Format("2006-01-02"), 2, 200, 100, 0.5)
	seedDailyUsage(t, repo, now.Format("2006-01-02"), 3, 300, 150, 1.5)

	tests := []struct {
		keyName      string
		query        string
		wantCount    int
		wantFirstFmt string
	}{
		{keyName: "today", query: "?period=today", wantCount: 24, wantFirstFmt: "15:04"},
		{keyName: "24h", query: "?period=24h", wantCount: 24, wantFirstFmt: "15:04"},
		{keyName: "7d", query: "?period=7d", wantCount: 7, wantFirstFmt: "Jan"},
		{keyName: "30d", query: "?period=30d", wantCount: 30, wantFirstFmt: "Jan"},
		{keyName: "60d", query: "?period=60d", wantCount: 60, wantFirstFmt: "Jan"},
		{keyName: "defaults to 7d", query: "", wantCount: 7, wantFirstFmt: "Jan"},
	}

	for _, tt := range tests {
		t.Run(tt.keyName, func(t *testing.T) {
			points, code := getChart(t, repo, tt.query)
			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}
			if len(points) != tt.wantCount {
				t.Fatalf("bucket count = %d, want %d", len(points), tt.wantCount)
			}
			if points[0].Label == "" {
				t.Fatal("first bucket has an empty label")
			}
		})
	}
}

// TestHandleUsageChart_AllPeriodSpansEveryDayOnRecord — "all" must start at the
// earliest stored day rather than a fixed 365-day window.
func TestHandleUsageChart_AllPeriodSpansEveryDayOnRecord(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	now := time.Now().UTC()
	seedDailyUsage(t, repo, now.AddDate(0, 0, -4).Format("2006-01-02"), 1, 10, 5, 0.1)
	seedDailyUsage(t, repo, now.Format("2006-01-02"), 1, 20, 10, 0.2)

	points, code := getChart(t, repo, "?period=all")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(points) != 5 {
		t.Fatalf("bucket count = %d, want 5 (earliest..today inclusive)", len(points))
	}
	if points[4].Requests != 1 || points[4].Tokens != 30 {
		t.Fatalf("last bucket = %+v, want the seeded today row", points[4])
	}
}

// TestHandleUsageChart_AllPeriodEmptyDatabase — no usage on record must return
// an empty array, never a null body the chart would fail to iterate.
func TestHandleUsageChart_AllPeriodEmptyDatabase(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	points, code := getChart(t, repo, "?period=all")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(points) != 0 {
		t.Fatalf("bucket count = %d, want 0", len(points))
	}
}

// TestHandleUsageChart_DailyBucketsSumTokensAndCost — the day rollup keeps
// prompt and completion tokens separate, so the chart's token figure is their
// sum and the cost is carried through unchanged.
func TestHandleUsageChart_DailyBucketsSumTokensAndCost(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	today := time.Now().UTC().Format("2006-01-02")
	seedDailyUsage(t, repo, today, 4, 1000, 250, 2.5)

	points, code := getChart(t, repo, "?period=7d")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	todayBucket := points[len(points)-1]
	if todayBucket.Tokens != 1250 {
		t.Errorf("tokens = %d, want 1250", todayBucket.Tokens)
	}
	if todayBucket.Cost != 2.5 {
		t.Errorf("cost = %v, want 2.5", todayBucket.Cost)
	}
	if todayBucket.Requests != 4 {
		t.Errorf("requests = %d, want 4", todayBucket.Requests)
	}
}

// TestHandleUsageChart_RejectsUnknownPeriod — an unrecognised period is a client
// error, matching upstream VALID_PERIODS returning 400.
func TestHandleUsageChart_RejectsUnknownPeriod(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/usage/chart?period=90d", nil)
	HandleUsageChart(repo)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestHandleUsageChart_HourlyBucketsAggregateHistory — the today/24h windows read
// usageHistory, and a row must land in exactly one hourly bucket.
func TestHandleUsageChart_HourlyBucketsAggregateHistory(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	for i := range 3 {
		if err := repo.InsertUsageHistory(
			"claude", "claude-sonnet", "conn-1", "sk-test", "/v1/messages",
			100, 25, 0.5, "success", 125, "{}", `{"prompt_tokens":100,"completion_tokens":25}`,
		); err != nil {
			t.Fatalf("insert usage history %d: %v", i, err)
		}
	}

	points, code := getChart(t, repo, "?period=24h")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	var totalRequests int
	var totalTokens int64
	for _, point := range points {
		totalRequests += point.Requests
		totalTokens += point.Tokens
	}
	if totalRequests != 3 {
		t.Errorf("total requests = %d, want 3", totalRequests)
	}
	if totalTokens != 375 {
		t.Errorf("total tokens = %d, want 375 (3 x 125)", totalTokens)
	}
}

// TestGetUsageDailyWithKeys_OldestFirstAndLimitedToRecentWindow — the chart
// needs the newest N days in chronological order, which GetUsageDailyRecent
// cannot provide (it drops the key and returns descending).
func TestGetUsageDailyWithKeys_OldestFirstAndLimitedToRecentWindow(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	base := time.Now().UTC().AddDate(0, 0, -4)
	for i := range 5 {
		seedDailyUsage(t, repo, base.AddDate(0, 0, i).Format("2006-01-02"), 1, 1, 1, 0.1)
	}

	rows, err := repo.GetUsageDailyWithKeys(2)
	if err != nil {
		t.Fatalf("GetUsageDailyWithKeys: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("row count = %d, want 2", len(rows))
	}
	if rows[0].DateKey >= rows[1].DateKey {
		t.Fatalf("rows are not chronological: %q then %q", rows[0].DateKey, rows[1].DateKey)
	}
	if rows[1].DateKey != base.AddDate(0, 0, 4).Format("2006-01-02") {
		t.Errorf("last row = %q, want the newest day", rows[1].DateKey)
	}

	all, err := repo.GetUsageDailyWithKeys(0)
	if err != nil {
		t.Fatalf("GetUsageDailyWithKeys(0): %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("unlimited row count = %d, want 5", len(all))
	}
	if all[0].DateKey != base.Format("2006-01-02") {
		t.Errorf("first row = %q, want the earliest day", all[0].DateKey)
	}
}
