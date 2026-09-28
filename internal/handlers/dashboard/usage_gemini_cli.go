package dashboard

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
)

// Gemini CLI usage — port of the gemini-cli half of
// open-sse/services/usage/google.js (getGeminiUsage).
//
// Quota comes from the Cloud Code Assist `retrieveUserQuota` RPC, which
// returns per-model buckets with a remaining FRACTION. There is no real
// request count to show, so rows are normalized onto a 1000 base — the same
// convention the Antigravity dashboard path already uses.
//
// Antigravity has its own path in usage.go and must not share this one: it
// reads a different RPC from a different host and applies a paid/free tier
// rule that does not apply here.

var (
	geminiCLIQuotaURL       = "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota"
	geminiCLILoadCodeAssist = "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
	geminiCLIQuotaTotalBase = 1000.0
	geminiCLIClientMetadata = map[string]any{"ideType": 9, "platform": 2, "pluginType": 2}
)

func fetchGeminiCLIUsage(ctx context.Context, accessToken string, psd map[string]any) usageResult {
	if strings.TrimSpace(accessToken) == "" {
		return usageResult{plan: "Free", message: "Gemini CLI access token not available.", bare: true}
	}

	// #1271 (upstream): the OAuth save stores projectId on the connection, not
	// inside providerSpecificData, so both are checked before spending a
	// loadCodeAssist call.
	projectID := psdStr(psd, "projectId")
	plan := "Free"
	if projectID == "" {
		info := fetchGeminiCLISubInfo(ctx, accessToken)
		projectID = antigravityProjectID(info["cloudaicompanionProject"])
		if tier, _ := info["currentTier"].(map[string]any); tier != nil {
			if name := usageStr(tier["name"]); name != "" {
				plan = name
			}
		}
	}

	if projectID == "" {
		return usageResult{
			plan:    plan,
			message: "Gemini CLI project ID not available. Reconnect Gemini CLI, or configure a Google Cloud project with Gemini Code Assist access before checking quota.",
			bare:    true,
		}
	}

	status, body, err := usagePost(ctx, geminiCLIQuotaURL, map[string]any{"project": projectID}, geminiCLIHeaders(accessToken))
	if err != nil {
		return usageResult{plan: plan, message: "Gemini CLI error: " + err.Error(), bare: true}
	}
	if status < 200 || status >= 300 {
		return usageResult{plan: plan, message: fmt.Sprintf("Gemini CLI quota error (%d).", status), bare: true}
	}

	quotas := make(map[string]any)
	if buckets, ok := body["buckets"].([]any); ok {
		for _, raw := range buckets {
			bucket, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			modelID := usageStr(bucket["modelId"])
			fraction, ok := usageFiniteNum(bucket["remainingFraction"])
			if modelID == "" || !ok {
				continue
			}
			total := geminiCLIQuotaTotalBase
			remaining := math.Round(total * fraction)
			quotas[modelID] = map[string]any{
				"used":                math.Max(0, total-remaining),
				"total":               total,
				"remainingPercentage": fraction * 100,
				"resetAt":             usageResetTimeToNil(bucket["resetTime"]),
				"unlimited":           false,
			}
		}
	}
	return usageResult{plan: plan, quotas: quotas}
}

func fetchGeminiCLISubInfo(ctx context.Context, accessToken string) map[string]any {
	status, data, err := usagePost(ctx, geminiCLILoadCodeAssist,
		map[string]any{"metadata": geminiCLIClientMetadata}, geminiCLIHeaders(accessToken))
	if err != nil || status != http.StatusOK || data == nil {
		return map[string]any{}
	}
	return data
}

func geminiCLIHeaders(accessToken string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + accessToken,
		"Content-Type":  "application/json",
	}
}
