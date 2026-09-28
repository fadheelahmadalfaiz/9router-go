package dashboard

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Zed usage — port of open-sse/services/usage/zed.js
// (getZedUsage + parseZedAuthenticatedUserUsage).
//
// Zed's auth header is non-standard: "Authorization: {user_id} {token}", not
// Bearer. Getting that wrong is a 401 that reads like an expired login.
//
// The plan payload mixes three limit encodings ("unlimited", a number, and
// {limited: N}) and one of them — model_requests.limit = 0 — does not mean an
// exhausted request quota at all: it means the plan bills hosted models per
// token. Rendering that as "0 / 100" would tell the user their model quota is
// gone.

var zedUsageURL = "https://cloud.zed.dev/client/users/me"

var (
	zedUnderscoreRe = regexp.MustCompile(`_`)
	zedSpacesRe     = regexp.MustCompile(`\s+`)
)

func fetchZedUsage(ctx context.Context, accessToken string, psd map[string]any) usageResult {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return usageResult{message: "Zed access token not available. Re-connect Zed to view quota.", bare: true}
	}
	userID := psdStr(psd, "userId")
	if userID == "" {
		return usageResult{message: "Zed credential is missing user id. Re-connect Zed to view quota.", bare: true}
	}

	status, _, out, err := usageGet(ctx, zedUsageURL, map[string]string{
		"Accept":        "application/json",
		"Authorization": userID + " " + accessToken,
	})
	if err != nil {
		return usageResult{message: "Zed error: " + err.Error(), bare: true}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return usageResult{message: "Zed authentication failed. Sign in again from the dashboard or Zed editor.", bare: true}
	}
	if status < 200 || status >= 300 {
		return usageResult{message: fmt.Sprintf("Zed error: quota API returned %d", status), bare: true}
	}
	return zedUsageResult(usageJSON(out))
}

type zedLimit struct {
	unlimited bool
	total     float64
}

// zedParseLimit accepts all three encodings Zed mixes into one field.
func zedParseLimit(raw any) zedLimit {
	if raw == nil {
		return zedLimit{}
	}
	if s, ok := raw.(string); ok {
		trimmed := strings.TrimSpace(s)
		if trimmed == "unlimited" {
			return zedLimit{unlimited: true}
		}
		if n, ok := usageFiniteNum(trimmed); ok {
			return zedLimit{total: math.Max(0, n)}
		}
	}
	if s, ok := raw.(string); ok && s == "unlimited" {
		return zedLimit{unlimited: true}
	}
	if n, ok := usageFiniteNum(raw); ok {
		return zedLimit{total: math.Max(0, n)}
	}
	if m, ok := raw.(map[string]any); ok {
		if u, _ := m["unlimited"].(bool); u {
			return zedLimit{unlimited: true}
		}
		if n, ok := usageFiniteNum(firstAny(m, "limited", "Limited")); ok {
			return zedLimit{total: math.Max(0, n)}
		}
	}
	return zedLimit{}
}

func zedUsageResult(userInfo map[string]any) usageResult {
	plan := nestedMap(userInfo, "plan")
	planID := firstAny(plan, "plan_v3", "plan_v2", "plan")
	if s, _ := planID.(string); s == "" {
		planID = firstAny(userInfo, "plan_v3")
	}
	resetAt := firstNonEmptyStr(
		usageResetTime(nestedMap(plan, "subscription_period")["ended_at"]),
		usageResetTime(nestedMap(plan, "subscriptionPeriod")["endedAt"]),
	)

	quotas := map[string]any{}
	usage := nestedMap(plan, "usage")
	if predictions := firstAny(usage, "edit_predictions", "editPredictions"); predictions != nil {
		bucket := nestedMapValue(predictions)
		quotas["Edit Predictions"] = zedQuotaRow(bucket["used"], bucket["limit"], resetAt)
	}
	if requests := firstAny(usage, "model_requests", "modelRequests"); requests != nil {
		bucket := nestedMapValue(requests)
		limitRaw := firstAny(bucket, "limit")
		if limitRaw == nil {
			limitRaw = nestedMapValue(requests)["limit"]
		}
		// limit 0 on a token-billed plan is not a request quota at all.
		if info := zedParseLimit(limitRaw); info.unlimited || info.total > 0 {
			quotas["Hosted Model Requests"] = zedQuotaRow(bucket["used"], limitRaw, resetAt)
		}
	}

	label := zedPlanLabel(planID)
	if (plan["trial_started_at"] != nil || plan["trialStartedAt"] != nil) && !strings.Contains(strings.ToLower(label), "trial") {
		label += " (Trial active)"
	}

	res := usageResult{plan: label, quotas: quotas}
	if plan["has_overdue_invoices"] == true || plan["hasOverdueInvoices"] == true {
		res.message = "This Zed account has overdue invoices. Usage may be blocked until billing is resolved."
		res.bare = true
	}
	return res
}

func zedQuotaRow(usedRaw, limitRaw any, resetAt string) map[string]any {
	used := math.Max(0, usageNum(usedRaw, 0))
	info := zedParseLimit(limitRaw)
	quota := map[string]any{
		"used":      used,
		"resetAt":   usageResetTimeToNil(resetAt),
		"unlimited": info.unlimited,
	}
	switch {
	case info.unlimited:
		quota["total"] = float64(0)
		quota["remainingPercentage"] = float64(100)
	case info.total <= 0:
		quota["total"] = float64(0)
		quota["remainingPercentage"] = float64(0)
	default:
		clamped := math.Min(used, info.total)
		remaining := math.Max(0, info.total-clamped)
		quota["used"] = clamped
		quota["total"] = info.total
		quota["remainingPercentage"] = remaining / info.total * 100
	}
	return quota
}

var zedPlanNames = map[string]string{
	"zed_free":      "Zed Free",
	"zed_pro":       "Zed Pro",
	"zed_pro_trial": "Zed Pro Trial",
	"zed_student":   "Zed Student",
	"zed_business":  "Zed Business",
}

func zedPlanLabel(planID any) string {
	raw := strings.TrimSpace(usageStr(planID))
	if raw == "" {
		return "Zed"
	}
	if name, ok := zedPlanNames[strings.ToLower(raw)]; ok {
		return name
	}
	words := zedSpacesRe.Split(zedUnderscoreRe.ReplaceAllString(raw, " "), -1)
	for i, w := range words {
		runes := []rune(w)
		if len(runes) == 0 {
			continue
		}
		words[i] = string(unicode.ToUpper(runes[0])) + strings.ToLower(string(runes[1:]))
	}
	return strings.Join(words, " ")
}

// nestedMapValue treats a value that is already an object as itself, so both
// `usage.edit_predictions` and the `{limit, used}` wrapper shapes work.
func nestedMapValue(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

var _ = sort.Strings
