package dashboard

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// GitHub Copilot usage — port of open-sse/services/usage/github.js
// (getGitHubUsage). Two response shapes exist and both have to be handled:
// paid plans report quota_snapshots, free/limited plans report
// monthly_quotas + limited_user_quotas.
//
// The endpoint wants the GitHub OAuth token, NOT the Copilot exchange token
// that the chat path uses — swapping them is a 401 that looks like an expired
// login.

var githubUsageURL = "https://api.github.com/copilot_internal/user"

func githubUsageHeaders(accessToken string) map[string]string {
	return map[string]string{
		"Authorization":         "token " + accessToken,
		"Accept":                "application/json",
		"X-GitHub-Api-Version":  "2022-11-28",
		"User-Agent":            "GitHubCopilotChat/0.26.7",
		"Editor-Version":        "vscode/1.100.0",
		"Editor-Plugin-Version": "copilot-chat/0.26.7",
	}
}

func fetchGitHubUsage(ctx context.Context, accessToken string) usageResult {
	if strings.TrimSpace(accessToken) == "" {
		return usageResult{message: "No GitHub access token available. Please re-authorize the connection.", bare: true}
	}

	status, _, out, err := usageGet(ctx, githubUsageURL, githubUsageHeaders(accessToken))
	if err != nil {
		return usageResult{message: fmt.Sprintf("Failed to fetch GitHub usage: %v", err), bare: true}
	}
	if status < 200 || status >= 300 {
		return usageResult{message: "GitHub API error: " + usageErrorSnippet(out, status), bare: true}
	}

	data := usageJSON(out)
	if data == nil {
		return usageResult{message: "GitHub Copilot connected. Usage response was not JSON.", bare: true}
	}
	if snapshots, ok := data["quota_snapshots"].(map[string]any); ok {
		return githubSnapshotResult(data, snapshots)
	}
	_, hasMonthly := data["monthly_quotas"]
	_, hasLimited := data["limited_user_quotas"]
	if hasMonthly || hasLimited {
		return githubLimitedResult(data)
	}
	return usageResult{message: "GitHub Copilot connected. Unable to parse quota data.", bare: true}
}

func githubSnapshotResult(data, snapshots map[string]any) usageResult {
	resetAt := usageResetTimeToNil(data["quota_reset_date"])
	quotas := make(map[string]any, len(snapshots))
	for _, key := range []string{"chat", "completions", "premium_interactions"} {
		if _, ok := snapshots[key]; !ok {
			continue
		}
		snapshot, _ := snapshots[key].(map[string]any)
		quotas[key] = githubSnapshotQuota(snapshot, resetAt)
	}
	if len(quotas) == 0 {
		return usageResult{message: "GitHub Copilot connected. No quota snapshots were returned.", bare: true}
	}
	return usageResult{plan: usageStr(data["copilot_plan"]), quotas: quotas}
}

func githubSnapshotQuota(snapshot map[string]any, resetAt any) map[string]any {
	entitlement := usageNum(snapshot["entitlement"], 0)
	remaining := usageNum(snapshot["remaining"], 0)
	unlimited, _ := snapshot["unlimited"].(bool)
	return map[string]any{
		"used":      math.Max(0, entitlement-remaining),
		"total":     entitlement,
		"unlimited": unlimited,
		"resetAt":   resetAt,
	}
}

func githubLimitedResult(data map[string]any) usageResult {
	monthly, _ := data["monthly_quotas"].(map[string]any)
	used, _ := data["limited_user_quotas"].(map[string]any)
	resetAt := usageResetTimeToNil(data["limited_user_reset_date"])

	quotas := make(map[string]any, 2)
	for _, key := range []string{"chat", "completions"} {
		quotas[key] = map[string]any{
			"used":      usageNum(used[key], 0),
			"total":     usageNum(monthly[key], 0),
			"unlimited": false,
			"resetAt":   resetAt,
		}
	}
	return usageResult{plan: firstNonEmptyStr(usageStr(data["copilot_plan"]), usageStr(data["access_type_sku"])), quotas: quotas}
}
