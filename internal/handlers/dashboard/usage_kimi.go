package dashboard

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
)

// Kimi Coding usage — port of open-sse/services/usage/kimi.js
// (getKimiUsage). One provider id, two auth shapes: a platform API key uses
// x-api-key, while device-code OAuth uses Bearer plus the X-Msh-* client
// headers. Chat sends both; /usages is Bearer-only for OAuth.

var kimiUsageURL = "https://api.kimi.com/coding/v1/usages"

var kimiPlanLevels = map[string]string{
	"LEVEL_BASIC":        "Moderato",
	"LEVEL_INTERMEDIATE": "Allegretto",
	"LEVEL_ADVANCED":     "Allegro",
	"LEVEL_STANDARD":     "Vivace",
}

var kimiPermissionRe = regexp.MustCompile(`(?i)permission_denied|do not have permission|subscribe`)

func fetchKimiUsage(ctx context.Context, accessToken, apiKey string, psd map[string]any) usageResult {
	apiKey = strings.TrimSpace(apiKey)
	accessToken = strings.TrimSpace(accessToken)
	if apiKey == "" && accessToken == "" {
		return usageResult{message: "Kimi access token or API key not available.", bare: true}
	}

	headers := kimiUsageHeaders(apiKey, accessToken, psd)
	status, _, out, err := usageGet(ctx, kimiUsageURL, headers)
	if err != nil {
		return usageResult{message: "Kimi Coding connected. Unable to fetch usage: " + err.Error(), bare: true}
	}
	if status < 200 || status >= 300 {
		return usageResult{plan: "Kimi Coding", message: kimiErrorMessage(status, out), bare: true}
	}

	data := usageJSON(out)
	if data == nil {
		return usageResult{plan: "Kimi Coding", message: "Kimi Coding connected. Invalid JSON response from API.", bare: true}
	}

	quotas := kimiQuotas(data)
	plan := kimiPlanName(nestedMap(nestedMap(data, "user"), "membership")["level"])
	if plan == "" {
		plan = "Kimi Coding"
	}
	if len(quotas) == 0 {
		return usageResult{plan: plan, message: "Kimi Coding connected. Usage tracked per request.", bare: true}
	}
	return usageResult{plan: plan, quotas: quotas}
}

func kimiUsageHeaders(apiKey, accessToken string, psd map[string]any) map[string]string {
	headers := map[string]string{"Content-Type": "application/json", "Accept": "application/json"}
	if apiKey != "" {
		headers["x-api-key"] = apiKey
		return headers
	}
	headers["Authorization"] = "Bearer " + accessToken
	for k, v := range kimiClientHeaders(psdStr(psd, "deviceId")) {
		headers[k] = v
	}
	return headers
}

// kimiClientHeaders mirrors buildKimiHeaders: the OAuth usage endpoint expects
// the same client identity the official CLI sends.
func kimiClientHeaders(deviceID string) map[string]string {
	deviceModel := fmt.Sprintf("%s %s", runtime.GOOS, runtime.GOARCH)
	switch runtime.GOOS {
	case "darwin":
		deviceModel = "macOS " + runtime.GOARCH
	case "windows":
		deviceModel = "Windows " + runtime.GOARCH
	case "linux":
		deviceModel = "Linux " + runtime.GOARCH
	}
	deviceName, err := os.Hostname()
	if err != nil || deviceName == "" {
		deviceName = "unknown"
	}
	if deviceID == "" {
		deviceID = "kimi-9router-go"
	}
	return map[string]string{
		"X-Msh-Platform":     "9router",
		"X-Msh-Version":      usageUserAgent,
		"X-Msh-Device-Name":  deviceName,
		"X-Msh-Device-Model": deviceModel,
		"X-Msh-Device-Id":    deviceID,
	}
}

func kimiPlanName(level any) string {
	key, _ := level.(string)
	if key == "" {
		return ""
	}
	if name, ok := kimiPlanLevels[key]; ok {
		return name
	}
	return strings.ToLower(strings.TrimPrefix(key, "LEVEL_"))
}

// kimiErrorMessage separates an expired session from a missing entitlement.
// A live OAuth token on an account without Kimi Code returns 403
// REASON_FEATURE_NO_PERMISSION — telling the user to re-authorize there would
// be wrong, since the token is fine.
func kimiErrorMessage(status int, body []byte) string {
	parsed := usageJSON(body)
	var localized, reason string
	if details, ok := firstAny(parsed, "details").([]any); ok && len(details) > 0 {
		if detail0, ok := details[0].(map[string]any); ok {
			reason = usageStr(nestedMap(detail0, "debug")["reason"])
			localized = usageStr(nestedMap(nestedMap(detail0, "localizedMessage"), "message"))
		}
	}
	if reason == "" {
		reason = usageStr(parsed["reason"])
	}
	if localized == "" {
		localized = usageStr(parsed["message"])
	}

	if status == http.StatusUnauthorized {
		return "Kimi authentication expired. Please re-authorize."
	}
	if status == http.StatusForbidden &&
		(reason == "REASON_FEATURE_NO_PERMISSION" || kimiPermissionRe.MatchString(string(body))) {
		return firstNonEmptyStr(localized,
			"Kimi connected, but this account has no permission to view usage. Subscribe to Kimi Code to access quota.")
	}

	snippet := firstNonEmptyStr(localized, strings.TrimSpace(string(body)))
	if len(snippet) > 100 {
		snippet = snippet[:100]
	}
	if snippet != "" {
		return fmt.Sprintf("Kimi Coding connected. API Error %d: %s", status, snippet)
	}
	return fmt.Sprintf("Kimi Coding connected. API Error %d", status)
}

func kimiQuotas(data map[string]any) map[string]any {
	quotas := map[string]any{}
	usage := nestedMap(data, "usage")

	limit := usageNum(firstAny(usage, "limit", "Limit"), 0)
	if limit > 0 {
		used := usageNum(firstAny(usage, "used", "Used"), 0)
		// `remaining` is optional; when absent it is derived from used/total.
		// Only remainingPercentage is emitted — QuotaTable reads an absolute
		// `remaining` as a 0-100 percentage and would invert the bar.
		remaining := usageNum(usage["remaining"], math.Max(0, limit-used))
		quotas["Weekly"] = map[string]any{
			"used":                math.Max(0, used),
			"total":               limit,
			"remainingPercentage": math.Max(0, math.Min(100, remaining/limit*100)),
			"resetAt":             usageResetTimeToNil(kimiResetTime(usage)),
			"unlimited":           false,
		}
	}

	if limits, ok := data["limits"].([]any); ok {
		for _, raw := range limits {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			detail := nestedMap(item, "detail")
			itemLimit := usageNum(firstAny(detail, "limit", "Limit"), 0)
			if itemLimit <= 0 {
				continue
			}
			remaining, ok := usageFiniteNum(firstAny(detail, "remaining", "Remaining"))
			if !ok {
				remaining = itemLimit
			}
			remaining = math.Max(0, math.Min(itemLimit, remaining))
			quotas["Ratelimit"] = map[string]any{
				"used":                math.Max(0, itemLimit-remaining),
				"total":               itemLimit,
				"remainingPercentage": remaining / itemLimit * 100,
				"resetAt":             usageResetTimeToNil(kimiResetTime(detail)),
				"unlimited":           false,
			}
			// One rate-limit row is all the dashboard can show; the last one
			// wins, matching upstream's single-key assignment.
		}
	}
	return quotas
}

func kimiResetTime(m map[string]any) any {
	return firstAny(m, "resetTime", "reset_time", "resetAt")
}
