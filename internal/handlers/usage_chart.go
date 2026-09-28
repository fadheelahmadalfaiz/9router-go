package handlers

import (
	"9router/proxy/internal/db"
	"9router/proxy/internal/handlerutil"
	json "encoding/json/v2"
	"net/http"
	"slices"
	"time"
)

// UsageChartPoint is one bucket of the usage time series, matching the shape
// upstream /api/usage/chart returns (src/lib/db/repos/usageRepo.js getChartData)
// so the dashboard chart port needs no adapter.
type UsageChartPoint struct {
	Label    string  `json:"label"`
	Tokens   int64   `json:"tokens"`
	Cost     float64 `json:"cost"`
	Requests int     `json:"requests"`
}

var usageChartPeriods = []string{"today", "24h", "7d", "30d", "60d", "all"}

const (
	usageChartHourBuckets = 24
	usageChartHourSpan    = time.Hour
	usageChartDaySpan     = 24 * time.Hour
)

// HandleUsageChart returns a period-bucketed token/cost/requests time series.
// "today" and "24h" bucket by hour from usageHistory; the longer periods bucket
// by day from the usageDaily rollup, which is what /api/usage/stats reads too.
func HandleUsageChart(repo *db.Repo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		period := r.URL.Query().Get("period")
		if period == "" {
			period = "7d"
		}
		if !slices.Contains(usageChartPeriods, period) {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "Invalid period")
			return
		}

		handlerutil.WriteJSON(w, http.StatusOK, buildUsageChart(repo, period))
	}
}

func buildUsageChart(repo *db.Repo, period string) []UsageChartPoint {
	if period == "today" || period == "24h" {
		return buildHourlyUsageChart(repo, period == "24h")
	}
	if period == "all" {
		return buildAllDailyUsageChart(repo)
	}
	days := 7
	switch period {
	case "30d":
		days = 30
	case "60d":
		days = 60
	}
	return buildDailyUsageChart(repo, days)
}

func buildHourlyUsageChart(repo *db.Repo, rolling bool) []UsageChartPoint {
	now := time.Now().UTC()
	start := now.Add(-usageChartHourSpan * usageChartHourBuckets)
	if !rolling {
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	}

	points := make([]UsageChartPoint, usageChartHourBuckets)
	for i := range points {
		points[i] = UsageChartPoint{Label: start.Add(time.Duration(i) * usageChartHourSpan).Format("15:04")}
	}

	rows, err := repo.GetUsageHistorySince(start.Format(time.RFC3339))
	if err != nil {
		return points
	}

	for _, row := range rows {
		at, err := time.Parse(time.RFC3339, row.Timestamp)
		if err != nil {
			continue
		}
		at = at.UTC()
		// A rolling window ends at "now", while a day window ends at midnight;
		// anything past the window end has no bucket to land in.
		if at.Before(start) {
			continue
		}
		idx := int(at.Sub(start) / usageChartHourSpan)
		if idx >= usageChartHourBuckets {
			continue
		}
		points[idx].Tokens += int64(row.PromptTokens) + int64(row.CompletionTokens)
		points[idx].Cost += row.Cost
		points[idx].Requests++
	}
	return points
}

func buildDailyUsageChart(repo *db.Repo, days int) []UsageChartPoint {
	today := time.Now().UTC().Truncate(usageChartDaySpan)
	start := today.Add(-usageChartDaySpan * time.Duration(days-1))

	points := make([]UsageChartPoint, days)
	byKey := make(map[string]int, days)
	for i := range points {
		day := start.Add(usageChartDaySpan * time.Duration(i))
		byKey[day.Format("2006-01-02")] = i
		points[i] = UsageChartPoint{Label: day.Format("Jan 2")}
	}
	fillUsageChartDays(repo, points, byKey)
	return points
}

func buildAllDailyUsageChart(repo *db.Repo) []UsageChartPoint {
	rows, err := repo.GetUsageDailyWithKeys(0)
	if err != nil || len(rows) == 0 {
		return []UsageChartPoint{}
	}

	earliest, err := time.Parse("2006-01-02", rows[0].DateKey)
	if err != nil {
		return []UsageChartPoint{}
	}
	earliest = earliest.UTC()
	today := time.Now().UTC().Truncate(usageChartDaySpan)
	days := int(today.Sub(earliest)/usageChartDaySpan) + 1
	if days < 1 {
		days = 1
	}

	points := make([]UsageChartPoint, days)
	byKey := make(map[string]int, days)
	for i := range points {
		day := earliest.Add(usageChartDaySpan * time.Duration(i))
		byKey[day.Format("2006-01-02")] = i
		points[i] = UsageChartPoint{Label: day.Format("Jan 2")}
	}
	fillUsageChartDays(repo, points, byKey)
	return points
}

// fillUsageChartDays folds the usageDaily rollup into the pre-built buckets.
// A day with no row keeps its zeroed bucket so the axis stays evenly spaced.
func fillUsageChartDays(repo *db.Repo, points []UsageChartPoint, byKey map[string]int) {
	rows, err := repo.GetUsageDailyWithKeys(len(points))
	if err != nil {
		return
	}
	for _, row := range rows {
		idx, ok := byKey[row.DateKey]
		if !ok {
			continue
		}
		var day struct {
			Requests         int     `json:"requests"`
			PromptTokens     int64   `json:"promptTokens"`
			CompletionTokens int64   `json:"completionTokens"`
			Cost             float64 `json:"cost"`
		}
		if err := json.Unmarshal([]byte(row.Data), &day); err != nil {
			continue
		}
		points[idx].Tokens += day.PromptTokens + day.CompletionTokens
		points[idx].Cost += day.Cost
		points[idx].Requests += day.Requests
	}
}
