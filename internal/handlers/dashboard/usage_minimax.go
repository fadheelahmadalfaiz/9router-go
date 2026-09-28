package dashboard

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/samber/lo"
)

// MiniMax usage fetcher — port of open-sse/services/usage/minimax.js
// (getMiniMaxUsage). Without this case the tracker answered with the empty
// lock fallback, so every MiniMax account rendered "Account active. No quota
// limits tracked." instead of its real M-series / Video windows.
//
// A var, not a const, so tests can point it at an httptest server.
var miniMaxUsageURLs = map[string][]string{
	"minimax": {
		"https://www.minimax.io/v1/token_plan/remains",
		"https://api.minimax.io/v1/api/openplatform/coding_plan/remains",
	},
	"minimax-cn": {
		"https://www.minimaxi.com/v1/api/openplatform/coding_plan/remains",
		"https://api.minimaxi.com/v1/api/openplatform/coding_plan/remains",
	},
}

// miniMaxAuthScanLimit caps how much of a response body is scanned for
// auth-failure wording, so a large error page cannot turn into a slow regexp.
const miniMaxAuthScanLimit = 2048

// miniMaxAuthPattern matches the upstream payloads that mean "this key cannot
// report a quota" rather than "this request failed". Upstream returns the
// invalid-key message for those instead of walking the fallback URL list.
var miniMaxAuthPattern = regexp.MustCompile(`(?i)token plan|coding plan|invalid api key|invalid key|unauthorized|inactive`)

var (
	miniMaxSepRe   = regexp.MustCompile(`[-_]+`)
	miniMaxSpaceRe = regexp.MustCompile(`\s+`)
	miniMaxWordRe  = regexp.MustCompile(`\b\w`)
	// The generic title-case above turns these into "To"/"Tts"/"Hd"; upstream
	// restores the acronym spellings afterwards.
	miniMaxTitleFixes = []struct {
		re   *regexp.Regexp
		with string
	}{
		{regexp.MustCompile(`\bTo\b`), "to"},
		{regexp.MustCompile(`\bTts\b`), "TTS"},
		{regexp.MustCompile(`\bHd\b`), "HD"},
	}
)

// miniMaxWindow is one resolved quota window (5h or 7d) of one model bucket.
type miniMaxWindow struct {
	total    float64
	count    float64
	provided *float64
	resetAt  string
}

func miniMaxHeaders(apiKey string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Accept":        "application/json",
		"Content-Type":  "application/json",
	}
}

func fetchMiniMaxUsage(ctx context.Context, apiKey, provider string) usageResult {
	if strings.TrimSpace(apiKey) == "" {
		return usageResult{message: "MiniMax API key not available.", bare: true}
	}

	usageURLs := miniMaxUsageURLs[provider]
	if len(usageURLs) == 0 {
		return miniMaxFetchFailed("")
	}

	headers := miniMaxHeaders(apiKey)
	for i, usageURL := range usageURLs {
		res, retryable := miniMaxAttempt(ctx, usageURL, headers)
		if retryable && i < len(usageURLs)-1 {
			continue
		}
		return res
	}
	return miniMaxFetchFailed("")
}

func miniMaxFetchFailed(detail string) usageResult {
	if detail == "" {
		return usageResult{message: "MiniMax connected. Unable to fetch usage.", bare: true}
	}
	return usageResult{message: "MiniMax connected. Unable to fetch usage: " + detail, bare: true}
}

// miniMaxAttempt performs one endpoint call. The bool reports whether the
// caller may try the next URL in the list.
func miniMaxAttempt(ctx context.Context, usageURL string, headers map[string]string) (usageResult, bool) {
	status, _, out, err := usageGet(ctx, usageURL, headers)
	if err != nil {
		return miniMaxFetchFailed(err.Error()), true
	}

	payload := usageJSON(out)
	if payload == nil {
		payload = map[string]any{}
	}
	rawText := string(out)
	if len(rawText) > miniMaxAuthScanLimit {
		rawText = rawText[:miniMaxAuthScanLimit]
	}

	baseResp := firstMap(payload, "base_resp", "baseResp")
	apiStatusCode := int(usageNum(firstAny(baseResp, "status_code", "statusCode"), 0))
	apiStatusMessage := strings.TrimSpace(usageStr(firstAny(baseResp, "status_msg", "statusMsg")))

	if status == http.StatusUnauthorized || status == http.StatusForbidden || apiStatusCode == 1004 ||
		miniMaxAuthPattern.MatchString(strings.TrimSpace(apiStatusMessage+" "+rawText)) {
		return usageResult{
			message: "MiniMax API key invalid or inactive. Use an active Token/Coding Plan key.",
			bare:    true,
		}, false
	}

	if status < 200 || status >= 300 {
		// 404/405/5xx are "this host does not serve the plan endpoint", so the
		// next URL is worth trying; anything else is a real rejection.
		return miniMaxEndpointError(status), status == http.StatusNotFound ||
			status == http.StatusMethodNotAllowed || status >= 500
	}

	if apiStatusCode != 0 {
		if apiStatusMessage == "" {
			apiStatusMessage = "Upstream quota API error"
		}
		return usageResult{message: "MiniMax connected. " + apiStatusMessage, bare: true}, false
	}

	quotaModels := lo.Filter(miniMaxModelList(payload), func(model map[string]any, _ int) bool {
		return miniMaxHasQuota(model)
	})
	if len(quotaModels) == 0 {
		return usageResult{message: "MiniMax connected. No quota data was returned.", bare: true}, false
	}

	// Only the coding_plan endpoint reports the count as "remaining"; on
	// token_plan the same field means "used". Getting this backwards flips the
	// whole bar, so it is derived from the URL exactly like upstream.
	countMeansRemaining := strings.Contains(usageURL, "/coding_plan/remains")

	quotas := make(map[string]any, len(quotaModels)*2)
	now := time.Now()
	for _, model := range quotaModels {
		displayName := miniMaxQuotaName(model)
		addMiniMaxQuota(quotas, displayName+" (5h)", miniMaxSessionWindow(model, now), countMeansRemaining)
		addMiniMaxQuota(quotas, displayName+" (7d)", miniMaxWeeklyWindow(model, now), countMeansRemaining)
	}

	if len(quotas) == 0 {
		return usageResult{message: "MiniMax connected. Unable to extract quota usage.", bare: true}, false
	}
	return usageResult{quotas: quotas}, false
}

func miniMaxEndpointError(status int) usageResult {
	return usageResult{
		message: fmt.Sprintf("MiniMax connected. MiniMax usage endpoint error (%d)", status),
		bare:    true,
	}
}

// ---------- payload accessors ----------

func miniMaxModelList(payload map[string]any) []map[string]any {
	raw, ok := firstAny(payload, "model_remains", "modelRemains").([]any)
	if !ok {
		return nil
	}
	return lo.FilterMap(raw, func(item any, _ int) (map[string]any, bool) {
		model, ok := item.(map[string]any)
		return model, ok
	})
}

// firstAny returns the first key that is present and non-nil. Every MiniMax
// payload arrives in either snake_case or camelCase depending on the host, so
// each field is read through both spellings.
func firstAny(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

func firstMap(m map[string]any, keys ...string) map[string]any {
	if n, ok := firstAny(m, keys...).(map[string]any); ok {
		return n
	}
	return map[string]any{}
}

func miniMaxTotal(model map[string]any, snakeKey, camelKey string) float64 {
	return math.Max(0, usageNum(firstAny(model, snakeKey, camelKey), 0))
}

// miniMaxPercent returns the upstream percentage when the field is present and
// finite, clamped to 0..100, and nil otherwise.
func miniMaxPercent(model map[string]any, snakeKey, camelKey string) *float64 {
	raw := firstAny(model, snakeKey, camelKey)
	if raw == nil {
		return nil
	}
	num, ok := usageFiniteNum(raw)
	if !ok {
		return nil
	}
	return lo.ToPtr(math.Max(0, math.Min(100, num)))
}

func miniMaxSessionTotal(model map[string]any) float64 {
	return miniMaxTotal(model, "current_interval_total_count", "currentIntervalTotalCount")
}

func miniMaxWeeklyTotal(model map[string]any) float64 {
	return miniMaxTotal(model, "current_weekly_total_count", "currentWeeklyTotalCount")
}

// miniMaxHasQuota keeps buckets that carry any usable signal. Old payloads
// report real counts; M3-era M-series buckets are percent-only (counts are 0),
// so those must be accepted too or the row disappears.
func miniMaxHasQuota(model map[string]any) bool {
	if miniMaxSessionTotal(model) > 0 || miniMaxWeeklyTotal(model) > 0 {
		return true
	}
	return miniMaxPercent(model, "current_interval_remaining_percent", "currentIntervalRemainingPercent") != nil ||
		miniMaxPercent(model, "current_weekly_remaining_percent", "currentWeeklyRemainingPercent") != nil
}

func miniMaxSessionWindow(model map[string]any, now time.Time) miniMaxWindow {
	return miniMaxWindow{
		total:    miniMaxSessionTotal(model),
		count:    usageNum(firstAny(model, "current_interval_usage_count", "currentIntervalUsageCount"), 0),
		provided: miniMaxPercent(model, "current_interval_remaining_percent", "currentIntervalRemainingPercent"),
		resetAt: miniMaxResetAt(model, now,
			"remains_time", "remainsTime", "end_time", "endTime"),
	}
}

func miniMaxWeeklyWindow(model map[string]any, now time.Time) miniMaxWindow {
	return miniMaxWindow{
		total:    miniMaxWeeklyTotal(model),
		count:    usageNum(firstAny(model, "current_weekly_usage_count", "currentWeeklyUsageCount"), 0),
		provided: miniMaxPercent(model, "current_weekly_remaining_percent", "currentWeeklyRemainingPercent"),
		resetAt: miniMaxResetAt(model, now,
			"weekly_remains_time", "weeklyRemainsTime", "weekly_end_time", "weeklyEndTime"),
	}
}

// miniMaxResetAt prefers the relative "remains in N ms" field, which is the only
// one MiniMax always sends, and falls back to the absolute end timestamp.
func miniMaxResetAt(model map[string]any, now time.Time, remainsSnake, remainsCamel, endSnake, endCamel string) string {
	remainsMs := usageNum(firstAny(model, remainsSnake, remainsCamel), 0)
	// A window cannot be further out than a year; a bigger number is a broken
	// field, not a real countdown, and would overflow the duration below.
	if remainsMs > 0 && remainsMs <= float64(365*24*time.Hour/time.Millisecond) {
		return now.Add(time.Duration(remainsMs) * time.Millisecond).UTC().Format(time.RFC3339)
	}
	return usageResetTime(firstAny(model, endSnake, endCamel))
}

// ---------- rendering ----------

func addMiniMaxQuota(quotas map[string]any, key string, w miniMaxWindow, countMeansRemaining bool) {
	total := w.total
	provided := w.provided
	if total <= 0 && provided == nil {
		return
	}

	count := math.Max(0, w.count)
	if total <= 0 {
		// Percent-only bucket: normalize onto a 100 base. The synthetic count
		// must carry the same meaning as the real field, otherwise the
		// computed percentage comes out inverted.
		total = 100
		if countMeansRemaining {
			count = math.Round(total * (*provided / 100))
		} else {
			count = math.Round(total * (1 - *provided/100))
		}
	}

	var used float64
	if countMeansRemaining {
		used = math.Max(total-count, 0)
	} else {
		used = math.Min(math.Max(0, count), total)
	}
	remaining := math.Max(total-used, 0)

	quota := map[string]any{
		"used":                used,
		"total":               total,
		"remaining":           remaining,
		"remainingPercentage": miniMaxRemainingPercentage(provided, remaining, total),
		"resetAt":             nil,
		"unlimited":           false,
	}
	if w.resetAt != "" {
		quota["resetAt"] = w.resetAt
	}
	quotas[key] = quota
}

// miniMaxRemainingPercentage prefers the upstream percentage, because the
// percent-only buckets have no real total to divide by.
func miniMaxRemainingPercentage(provided *float64, remaining, total float64) float64 {
	if provided != nil {
		return *provided
	}
	if total <= 0 {
		return 0
	}
	return math.Max(0, math.Min(100, remaining/total*100))
}

// miniMaxQuotaName turns a raw bucket name into a dashboard label. M3+ reports
// every M-series model as one shared wildcard bucket ("MiniMax-M*", renamed to
// "general" in newer payloads); neither is a name worth showing raw.
func miniMaxQuotaName(model map[string]any) string {
	raw := strings.TrimSpace(usageStr(firstAny(model, "model_name", "modelName")))
	if raw == "" {
		return "MiniMax"
	}
	if raw == "MiniMax-M*" || raw == "general" {
		return "M-series"
	}

	spaced := miniMaxSpaceRe.ReplaceAllString(miniMaxSepRe.ReplaceAllString(raw, " "), " ")
	spaced = strings.TrimSpace(spaced)
	spaced = miniMaxWordRe.ReplaceAllStringFunc(spaced, strings.ToUpper)
	for _, fix := range miniMaxTitleFixes {
		spaced = fix.re.ReplaceAllString(spaced, fix.with)
	}
	return spaced
}
