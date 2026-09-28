package dashboard

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CodeBuddy quota — shared by the CN and international providers. Upstream has
// one getCodeBuddyUsage(providerId, …) behind two exported wrappers, and the
// only differences are the host, the IDE headers, and the wording.
//
// The Tencent billing endpoint mixes two credit types that must NOT be merged:
//   - Refill ("基础体验包"): a recurring allowance whose cycle resets long
//     before the resource expires (CycleEndTime << DeductionEndTime). Live
//     numbers live in the *Cycle* fields and resetAt is the next refresh.
//   - Bonus ("活动赠送包"): one-shot credits that run a single cycle and then
//     expire for good. Numbers live in the plain Capacity fields.
//
// One quota row per package: a cadence label (Monthly/Weekly/Daily) for refills,
// "Bonus Pack N" for bonuses, soonest-expiring first.

var codebuddyUsageURLs = map[string]string{
	"codebuddy-cn":   "https://copilot.tencent.com/v2/billing/meter/get-user-resource",
	"codebuddy-intl": "https://www.codebuddy.ai/v2/billing/meter/get-user-resource",
}

func codebuddyHeaders(ideType string) map[string]string {
	return map[string]string{
		"User-Agent":          "IDE/2.108.1 CodeBuddy/2.108.1",
		"X-Product":           "SaaS",
		"X-IDE-Type":          ideType,
		"X-IDE-Name":          ideType,
		"X-Requested-With":    "XMLHttpRequest",
		"X-Codebuddy-Request": "1",
		"Content-Type":        "application/json",
		"Accept":              "application/json",
	}
}

func codebuddyNum(precise, plain any) float64 {
	if s, ok := precise.(string); ok && strings.TrimSpace(s) != "" {
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f
		}
	}
	return usageNum(plain, 0)
}

func codebuddyCycleEndMs(acc map[string]any) float64 {
	if s := usageResetTime(acc["CycleEndTime"]); s != "" {
		if tm, err := time.Parse(time.RFC3339, s); err == nil {
			return float64(tm.UnixMilli())
		}
	}
	return math.Inf(1)
}

func codebuddyIsRefill(acc map[string]any) bool {
	ce := codebuddyCycleEndMs(acc)
	// DeductionEndTime arrives in unix MILLISECONDS (e.g. 1790429257000);
	// upstream compares it directly against the cycle-end ms.
	de, ok := usageFiniteNum(acc["DeductionEndTime"])
	if math.IsInf(ce, 1) || !ok {
		return false
	}
	return de-ce > float64(2*24*60*60*1000)
}

func codebuddyCadence(acc map[string]any) string {
	start, end := usageResetTime(acc["CycleStartTime"]), usageResetTime(acc["CycleEndTime"])
	if start != "" && end != "" {
		if ts, err1 := time.Parse(time.RFC3339, start); err1 == nil {
			if te, err2 := time.Parse(time.RFC3339, end); err2 == nil {
				days := te.Sub(ts).Hours() / 24
				if days <= 1.5 {
					return "Daily"
				}
				if days <= 10 {
					return "Weekly"
				}
			}
		}
	}
	return "Monthly"
}

func fetchCodeBuddyCnUsage(ctx context.Context, accessToken, apiKey string) usageResult {
	return fetchCodeBuddyUsage(ctx, "codebuddy-cn", "CLI", accessToken, apiKey)
}

func fetchCodeBuddyIntlUsage(ctx context.Context, accessToken, apiKey string) usageResult {
	return fetchCodeBuddyUsage(ctx, "codebuddy-intl", "IDE", accessToken, apiKey)
}

func fetchCodeBuddyUsage(ctx context.Context, providerID, ideType, accessToken, apiKey string) usageResult {
	token := firstNonEmptyStr(accessToken, apiKey)
	if token == "" {
		return usageResult{message: fmt.Sprintf("CodeBuddy (%s) credential not available.", providerID), bare: true}
	}

	headers := codebuddyHeaders(ideType)
	headers["Authorization"] = "Bearer " + token

	status, _, out, err := usageDo(ctx, http.MethodPost, codebuddyUsageURLs[providerID], headers, []byte("{}"))
	if err != nil {
		return usageResult{message: fmt.Sprintf("CodeBuddy (%s) error: %v", providerID, err), bare: true}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return usageResult{message: "CodeBuddy CN credential invalid or expired.", bare: true}
	}
	if status < 200 || status >= 300 {
		return usageResult{message: fmt.Sprintf("CodeBuddy CN quota API error (%d).", status), bare: true}
	}

	body := usageJSON(out)
	if body == nil {
		return usageResult{message: fmt.Sprintf("CodeBuddy (%s) error: invalid JSON", providerID), bare: true}
	}
	if code := usageNum(body["code"], -1); code != 0 {
		msg := usageStr(body["msg"])
		if msg == "" {
			msg = "unknown"
		}
		return usageResult{message: fmt.Sprintf("CodeBuddy CN quota error: %s", msg), bare: true}
	}

	accounts := codebuddyAccounts(body)
	if len(accounts) == 0 {
		return usageResult{message: "CodeBuddy CN connected. No credit package found.", bare: true}
	}

	var refills, bonuses []map[string]any
	for _, acc := range accounts {
		if codebuddyIsRefill(acc) {
			refills = append(refills, acc)
		} else {
			bonuses = append(bonuses, acc)
		}
	}
	byExpiry := func(list []map[string]any) func(i, j int) bool {
		return func(i, j int) bool { return codebuddyCycleEndMs(list[i]) < codebuddyCycleEndMs(list[j]) }
	}
	sort.SliceStable(refills, byExpiry(refills))
	sort.SliceStable(bonuses, byExpiry(bonuses))

	quotas := make(map[string]any, len(accounts))
	seen := map[string]int{}
	for _, acc := range refills {
		base := codebuddyCadence(acc)
		seen[base]++
		name := base
		if seen[base] > 1 {
			name = fmt.Sprintf("%s %d", base, seen[base])
		}
		quotas[name] = map[string]any{
			"used":    codebuddyNum(acc["CycleCapacityUsedPrecise"], acc["CycleCapacityUsed"]),
			"total":   codebuddyNum(acc["CycleCapacitySizePrecise"], acc["CycleCapacitySize"]),
			"resetAt": usageResetTimeToNil(acc["CycleEndTime"]),
			// Recurring allowance: CycleEndTime is the next refresh, not the
			// final expiry, so the UI must say "Resets in".
			"unlimited": false,
			"recurring": true,
		}
	}
	for i, acc := range bonuses {
		quotas[fmt.Sprintf("Bonus Pack %d", i+1)] = map[string]any{
			"used":    codebuddyNum(acc["CapacityUsedPrecise"], acc["CapacityUsed"]),
			"total":   codebuddyNum(acc["CapacitySizePrecise"], acc["CapacitySize"]),
			"resetAt": usageResetTimeToNil(acc["CycleEndTime"]),
			// One-shot credits: never replenish, so "Expires in".
			"unlimited": false,
			"recurring": false,
		}
	}

	return usageResult{plan: codebuddyPlan(refills, accounts), quotas: quotas}
}

// codebuddyAccounts unwraps the Tencent envelope, which nests twice:
// {data:{Response:{Data:{Accounts:[…]}}}}.
func codebuddyAccounts(body map[string]any) []map[string]any {
	data := nestedMap(nestedMap(body, "data"), "Response")
	accounts, _ := nestedMap(data, "Data")["Accounts"].([]any)
	out := make([]map[string]any, 0, len(accounts))
	for _, raw := range accounts {
		if acc, ok := raw.(map[string]any); ok {
			out = append(out, acc)
		}
	}
	return out
}

func codebuddyPlan(refills, accounts []map[string]any) string {
	base := map[string]any{}
	if len(refills) > 0 {
		base = refills[0]
	} else if len(accounts) > 0 {
		base = accounts[0]
	}
	return firstNonEmptyStr(usageStr(base["PackageName"]), usageStr(base["SubProductName"]), "CodeBuddy")
}
