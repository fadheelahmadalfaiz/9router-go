package dashboard

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Claude usage fetcher — port of open-sse/services/usage/claude.js
// (getClaudeUsage). Without this case every Claude Code account fell through to
// the empty lock fallback, so the tracker never showed its 5h / 7d windows.
//
// This is the most rate-limited quota endpoint in the fleet: Anthropic 429s it
// long before it 429s chat, and the tracker re-reads every visible connection
// each tick. Hence the TTL cache, the in-flight collapse, and the per-token
// cooldown.

const (
	claudeAPIVersion = "2023-06-01"

	claudeUsageCacheTTL = 5 * time.Minute
	claudeCooldownTTL   = 3 * time.Minute

	// claudeUsageCacheMax bounds the cache so churned connections cannot grow
	// it without end. Real deployments sit far below this.
	claudeUsageCacheMax = 256
)

// Vars, not consts, so tests can point them at an httptest server.
var (
	claudeOAuthUsageURL = "https://api.anthropic.com/api/oauth/usage"
	claudeSettingsURL   = "https://api.anthropic.com/v1/settings"
	claudeOrgUsageURL   = "https://api.anthropic.com/v1/organizations/%s/usage"
)

type claudeCacheEntry struct {
	result    usageResult
	expiresAt time.Time
}

var (
	claudeCacheMu sync.Mutex
	claudeCache   = map[string]claudeCacheEntry{}
	claudeFlight  singleflight.Group
	claudeCoolMu  sync.Mutex
	claudeCool    = map[string]time.Time{}
)

func fetchClaudeUsage(ctx context.Context, accessToken string, force bool) usageResult {
	if strings.TrimSpace(accessToken) == "" {
		return claudeMessage("Claude access token not available. Please re-authorize the connection.")
	}

	if !force {
		if res, ok := claudeCachedUsage(accessToken); ok {
			return res
		}
	}

	// Collapses concurrent readers of the same token onto one live call. The
	// shared context is the first caller's, which is upstream's own behavior
	// (it caches the in-flight promise per token too).
	v, _, _ := claudeFlight.Do(accessToken, func() (any, error) {
		return claudeSettle(accessToken, fetchClaudeUsageLive(ctx, accessToken)), nil
	})
	res, _ := v.(usageResult)
	return res
}

// claudeCachedUsage serves the last read while it is still inside the TTL.
// Only real quota data is ever cached, so a transient 429 cannot pin an error
// message on screen for five minutes.
func claudeCachedUsage(accessToken string) (usageResult, bool) {
	claudeCacheMu.Lock()
	defer claudeCacheMu.Unlock()
	entry, ok := claudeCache[accessToken]
	if !ok || time.Now().After(entry.expiresAt) {
		return usageResult{}, false
	}
	return entry.result, true
}

// claudeSettle decides what a live read leaves behind. Real quota rows are
// cached for the TTL; a soft failure (429, admin-only, network blip) keeps the
// last good read on screen rather than blanking real numbers, and caches
// nothing so the next tick retries.
func claudeSettle(accessToken string, res usageResult) usageResult {
	claudeCacheMu.Lock()
	defer claudeCacheMu.Unlock()
	if len(res.quotas) == 0 {
		if prev, ok := claudeCache[accessToken]; ok {
			return prev.result
		}
		return res
	}
	claudePruneLocked()
	claudeCache[accessToken] = claudeCacheEntry{result: res, expiresAt: time.Now().Add(claudeUsageCacheTTL)}
	return res
}

// claudePruneLocked makes room once the cache is full, so churned connections
// cannot grow it without end.
func claudePruneLocked() {
	for len(claudeCache) >= claudeUsageCacheMax {
		for token := range claudeCache {
			delete(claudeCache, token)
			break
		}
	}
}

func claudeInCooldown(accessToken string) bool {
	claudeCoolMu.Lock()
	defer claudeCoolMu.Unlock()
	until, ok := claudeCool[accessToken]
	if !ok {
		return false
	}
	if now := time.Now(); now.After(until) {
		delete(claudeCool, accessToken)
		return false
	}
	return true
}

func claudeMarkCooldown(accessToken string) {
	claudeCoolMu.Lock()
	defer claudeCoolMu.Unlock()
	claudeCool[accessToken] = time.Now().Add(claudeCooldownTTL)
}

func claudeMessage(msg string) usageResult {
	return usageResult{message: msg, bare: true}
}

// ---------- live reads ----------

func fetchClaudeUsageLive(ctx context.Context, accessToken string) usageResult {
	// A 429 rate-limits the quota endpoint only — chat with the same token
	// keeps working. While cooling down, read the org endpoint instead of
	// asking the endpoint that just said no.
	if claudeInCooldown(accessToken) {
		return claudeLegacyUsage(ctx, accessToken)
	}

	status, _, out, err := usageGet(ctx, claudeOAuthUsageURL, claudeOAuthHeaders(accessToken))
	if err != nil {
		return claudeMessage("Claude connected. Unable to fetch usage: " + err.Error())
	}
	if status >= 200 && status < 300 {
		return claudeParseOAuthUsage(usageJSON(out))
	}
	if status == http.StatusTooManyRequests {
		claudeMarkCooldown(accessToken)
	}
	return claudeLegacyUsage(ctx, accessToken)
}

func claudeOAuthHeaders(accessToken string) map[string]string {
	return map[string]string{
		"Authorization":     "Bearer " + accessToken,
		"anthropic-beta":    "oauth-2025-04-20",
		"anthropic-version": claudeAPIVersion,
	}
}

func claudeAPIHeaders(accessToken string) map[string]string {
	return map[string]string{
		"Authorization":     "Bearer " + accessToken,
		"anthropic-version": claudeAPIVersion,
	}
}

// claudeParseOAuthUsage turns the Claude Code OAuth quota payload into rows.
// utilization is a percentage USED, so 87 means 13% left — the field is not a
// remaining value, and inverting it silently doubles the reported usage.
func claudeParseOAuthUsage(data map[string]any) usageResult {
	quotas := make(map[string]any)
	if win, ok := claudeWindow(data["five_hour"]); ok {
		quotas["session (5h)"] = win
	}
	if win, ok := claudeWindow(data["seven_day"]); ok {
		quotas["weekly (7d)"] = win
	}
	// Model-specific weekly windows, e.g. seven_day_sonnet -> weekly sonnet (7d).
	// "seven_day" itself has no trailing underscore, so it never matches here.
	for key, raw := range data {
		model, isModelWindow := strings.CutPrefix(key, "seven_day_")
		if !isModelWindow || model == "" {
			continue
		}
		if win, ok := claudeWindow(raw); ok {
			quotas["weekly "+model+" (7d)"] = win
		}
	}

	return usageResult{
		plan:   "Claude Code",
		quotas: quotas,
		extra:  map[string]any{"extraUsage": data["extra_usage"]},
	}
}

func claudeWindow(raw any) (map[string]any, bool) {
	win, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	utilization, ok := usageFiniteNum(win["utilization"])
	if !ok {
		return nil, false
	}
	remaining := math.Max(0, 100-utilization)
	out := map[string]any{
		"used":                utilization,
		"total":               float64(100),
		"remaining":           remaining,
		"remainingPercentage": remaining,
		"resetAt":             nil,
		"unlimited":           false,
	}
	if resetAt := usageResetTime(win["resets_at"]); resetAt != "" {
		out["resetAt"] = resetAt
	}
	return out, true
}

// claudeLegacyUsage is the API-key / org-admin path: the settings endpoint
// reveals the org, and only an org admin can read its usage breakdown.
func claudeLegacyUsage(ctx context.Context, accessToken string) usageResult {
	status, _, out, err := usageGet(ctx, claudeSettingsURL, claudeAPIHeaders(accessToken))
	if err != nil {
		return claudeMessage("Claude connected. Unable to fetch usage: " + err.Error())
	}
	if status < 200 || status >= 300 {
		return claudeMessage("Claude connected. Usage API requires admin permissions.")
	}

	settings := usageJSON(out)
	res := usageResult{plan: firstNonEmptyStr(usageStr(settings["plan"]), "Unknown")}
	if org := usageStr(settings["organization_name"]); org != "" {
		res.extra = map[string]any{"organization": org}
	}

	orgID := usageStr(settings["organization_id"])
	if orgID == "" {
		res.message = "Claude connected. Usage details require admin access."
		res.bare = true
		return res
	}

	orgURL := fmt.Sprintf(claudeOrgUsageURL, url.PathEscape(orgID))
	usageStatus, _, usageOut, usageErr := usageGet(ctx, orgURL, claudeAPIHeaders(accessToken))
	if usageErr != nil {
		return claudeMessage("Claude connected. Unable to fetch usage: " + usageErr.Error())
	}
	if usageStatus < 200 || usageStatus >= 300 {
		res.message = "Claude connected. Usage details require admin access."
		res.bare = true
		return res
	}

	res.quotas = usageJSON(usageOut)
	return res
}
