package dashboard

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
)

// GLM Coding Plan usage — port of open-sse/services/usage/glm.js
// (getGlmUsage). One endpoint per region; both answer the same
// {data:{limits:[…], level}} shape.
//
// `percentage` is percent USED, and `unit` decides the row label: unit 3 is a
// session (keyed by its length in hours), unit 6 is weekly. Collapsing them
// into one "Tokens" row is what makes a GLM account look like it has a single
// limit when it actually has two independent windows.

var glmUsageURLs = map[string]string{
	"glm":    "https://api.z.ai/api/monitor/usage/quota/limit",
	"glm-cn": "https://open.bigmodel.cn/api/monitor/usage/quota/limit",
}

func fetchGlmUsage(ctx context.Context, apiKey, provider string) usageResult {
	if strings.TrimSpace(apiKey) == "" {
		return usageResult{message: "GLM API key not available.", bare: true}
	}
	quotaURL, ok := glmUsageURLs[provider]
	if !ok {
		return usageResult{message: "GLM error: unknown region " + provider, bare: true}
	}

	status, _, out, err := usageGet(ctx, quotaURL, map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Accept":        "application/json",
	})
	if err != nil {
		return usageResult{message: "GLM error: " + err.Error(), bare: true}
	}
	if status == http.StatusUnauthorized {
		return usageResult{message: "GLM API key invalid or expired.", bare: true}
	}
	if status < 200 || status >= 300 {
		return usageResult{message: fmt.Sprintf("GLM quota API error (%d).", status), bare: true}
	}

	payload := usageJSON(out)
	data := nestedMap(payload, "data")
	quotas := make(map[string]any)
	if limits, ok := data["limits"].([]any); ok {
		for _, raw := range limits {
			limit, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			// GLM mixes a token budget with a credit budget; only these two
			// types describe a plan limit.
			kind := usageStr(limit["type"])
			if kind != "TOKENS_LIMIT" && kind != "CREDIT_LIMIT" {
				continue
			}
			usedPercent := usageNum(limit["percentage"], 0)
			remaining := math.Max(0, 100-usedPercent)
			quota := map[string]any{
				"used":                usedPercent,
				"total":               float64(100),
				"remaining":           remaining,
				"remainingPercentage": remaining,
				"resetAt":             nil,
				"unlimited":           false,
			}
			if resetMs := usageNum(limit["nextResetTime"], 0); resetMs > 0 {
				quota["resetAt"] = usageResetTime(resetMs)
			}
			quotas[glmLimitKey(limit, kind)] = quota
		}
	}
	return usageResult{plan: glmPlanName(usageStr(data["level"])), quotas: quotas}
}

// glmLimitKey names the window so two limits never overwrite each other.
func glmLimitKey(limit map[string]any, kind string) string {
	number := usageNum(limit["number"], 0)
	switch unit := usageNum(limit["unit"], 0); unit {
	case 3:
		return fmt.Sprintf("Session (%gh)", number)
	case 6:
		return "Weekly (7d)"
	}
	if kind == "TOKENS_LIMIT" {
		return "Tokens"
	}
	return fmt.Sprintf("Limit (%g)", number)
}

// glmPlanName upper-cases the first character and lower-cases the rest, the
// same shape upstream applies to data.level.
func glmPlanName(level string) string {
	if level == "" {
		return "Unknown"
	}
	return strings.ToUpper(level[:1]) + strings.ToLower(level[1:])
}
