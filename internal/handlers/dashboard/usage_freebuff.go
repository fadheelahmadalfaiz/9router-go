package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Freebuff usage — port of open-sse/services/usage/freebuff.js
// (getFreebuffUsage).
//
// 🔴 The read MUST be GET /api/v1/freebuff/session. POST on that endpoint
// CLAIMS a session and burns 1.0 unit of the daily quota — a quota tracker
// that spends quota to report quota is worse than one that reports nothing.
//
// Two quota shapes share the account:
//   - session pools: rateLimitsByModel, keyed by model id, on the pre-join
//     (`none`), `active` and `ended` states.
//   - Freebucks meter: a daily pool plus a wallet, replicated under every
//     priced model. Prices are server-authoritative, so nothing is hardcoded.

var freebuffUsageURL = "https://www.codebuff.com/api/v1/freebuff/session"

const freebuffUsageUA = "codebuff-cli/0.0.138"

var freebuffModelLabels = map[string]string{
	"z-ai/glm-5.3-flash":              "GLM 5.3 Flash",
	"deepseek/deepseek-v4-flash":      "DeepSeek V4.1 Flash",
	"openai/gpt-5.6-luna":             "GPT-5.6 Luna",
	"mimo/mimo-v2.5":                  "MiMo 2.5",
	"upstage/solar-pro4":              "Solar Pro 4",
	"meta/muse-spark-1.2-contributor": "Muse Spark 1.2",
	"anthropic/claude-fable-5":        "Claude Fable 5 (limited offer)",
}

var freebuffPriceChangeTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

func fetchFreebuffUsage(ctx context.Context, accessToken string) usageResult {
	if strings.TrimSpace(accessToken) == "" {
		return usageResult{message: "Freebuff credential not available — connect a Freebuff login first.", bare: true}
	}

	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
		"User-Agent":    freebuffUsageUA,
		"Accept":        "application/json",
	}
	status, _, out, err := usageGet(ctx, freebuffUsageURL, headers)
	if err != nil {
		return usageResult{message: "Freebuff usage error: " + err.Error(), bare: true}
	}

	// A 403 here is usually a server-side gate status, not a bad credential —
	// telling the user to re-login would send them down the wrong path.
	if status == http.StatusUnauthorized {
		return usageResult{message: "Freebuff credential invalid or expired — re-login in the dashboard.", bare: true}
	}
	if status == http.StatusForbidden {
		return freebuffForbidden(usageJSON(out))
	}
	if status == http.StatusNotFound {
		return usageResult{plan: "Freebuff", message: "Freebuff connected. No session quota to report right now.", bare: true}
	}
	if status < 200 || status >= 300 {
		return usageResult{message: fmt.Sprintf("Freebuff quota API error (%d).", status), bare: true}
	}

	data := usageJSON(out)
	quotas := freebuffQuotas(data)
	plan := "Freebuff"
	if usageStr(data["accessTier"]) == "limited" {
		plan = "Freebuff (Limited)"
	}
	if len(quotas) == 0 {
		return usageResult{plan: plan, message: "Freebuff connected. No session quota to report right now.", bare: true}
	}

	res := usageResult{plan: plan, quotas: quotas}
	if summary := freebuffSummary(nestedMap(data, "freebucks")); summary != nil {
		res.extra = map[string]any{"freebucks": summary}
	}
	return res
}

func freebuffForbidden(body map[string]any) usageResult {
	switch usageStr(body["status"]) {
	case "country_blocked":
		return usageResult{message: "Freebuff is not available in your region.", bare: true}
	case "banned":
		return usageResult{message: "Your Freebuff account has been banned.", bare: true}
	}
	msg := "Freebuff quota access denied (403)"
	if detail := usageStr(body["message"]); detail != "" {
		msg += ": " + detail
	}
	return usageResult{message: msg + ".", bare: true}
}

func freebuffQuotas(data map[string]any) map[string]any {
	quotas := map[string]any{}
	for model, rl := range freebuffRateLimits(data) {
		bucket, ok := rl.(map[string]any)
		if !ok {
			continue
		}
		quota := map[string]any{
			"used":      usageNum(bucket["recentCount"], 0),
			"total":     usageNum(bucket["limit"], 0),
			"resetAt":   usageResetTimeToNil(bucket["resetAt"]),
			"unlimited": false,
			// The daily/weekly Pacific session allowance replenishes at
			// resetAt, so the UI must say "Resets in", not "Expires in".
			"recurring": true,
		}
		if label, ok := freebuffModelLabels[model]; ok {
			quota["displayName"] = label
		}
		quotas[model] = quota
	}
	freebuffFreebucksRows(nestedMap(data, "freebucks"), quotas)
	return quotas
}

func freebuffRateLimits(data map[string]any) map[string]any {
	pool := map[string]any{}
	for model, raw := range nestedMap(data, "rateLimitsByModel") {
		pool[model] = raw
	}
	// An active session carries its own rateLimit row; older servers leave the
	// model out of the shared map, so fold it back in.
	if usageStr(data["status"]) == "active" {
		if model := usageStr(data["model"]); model != "" {
			if _, exists := pool[model]; !exists {
				if rl := data["rateLimit"]; rl != nil {
					pool[model] = rl
				}
			}
		}
	}
	return pool
}

// freebuffFreebucksRows replaces the session pools with the daily Freebucks
// pool, replicated under every priced model the way the official picker does.
func freebuffFreebucksRows(freebucks map[string]any, quotas map[string]any) {
	daily := nestedMap(freebucks, "daily")
	if len(daily) == 0 {
		return
	}
	spent := usageNum(daily["spent"], 0)
	limit := usageNum(daily["limit"], 0)
	prices := freebuffDuePrices(freebucks)
	notices := nestedMap(freebuffsOf(freebucks), "priceNotices")

	for model, priceRaw := range prices {
		quota := map[string]any{
			"used":      spent,
			"total":     limit,
			"resetAt":   usageResetTimeToNil(daily["resetAt"]),
			"unlimited": false,
			"recurring": true,
		}
		if price, ok := usageFiniteNum(priceRaw); ok {
			quota["price"] = price
		}
		if note := usageStr(notices[model]); note != "" {
			quota["priceNote"] = note
		}
		if label, ok := freebuffModelLabels[model]; ok {
			quota["displayName"] = label
		}
		quotas[model] = quota
	}
}

// freebuffDuePrices folds the server's announced price schedule into the live
// price map, so a promo expires and reverts on the server's timeline with no
// release from us. Nothing is hardcoded.
func freebuffDuePrices(freebucks map[string]any) map[string]any {
	prices := map[string]any{}
	for model, price := range nestedMap(freebucks, "prices") {
		prices[model] = price
	}
	now := time.Now()
	changes, _ := freebucks["priceChanges"].([]any)
	for _, raw := range changes {
		change, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		model := usageStr(change["modelId"])
		if _, tracked := prices[model]; !tracked {
			continue
		}
		at := usageStr(change["at"])
		if !freebuffPriceChangeTime.MatchString(at) {
			continue
		}
		due, err := time.Parse(time.RFC3339, at)
		if err != nil || due.After(now) {
			continue
		}
		prices[model] = change["price"]
	}
	return prices
}

func freebuffsOf(freebucks map[string]any) map[string]any { return freebucks }

func freebuffSummary(freebucks map[string]any) map[string]any {
	daily := nestedMap(freebucks, "daily")
	if len(daily) == 0 {
		return nil
	}
	summary := map[string]any{
		"daily": map[string]any{
			"limit":     usageNum(daily["limit"], 0),
			"spent":     usageNum(daily["spent"], 0),
			"remaining": usageNum(daily["remaining"], 0),
			"resetAt":   usageResetTimeToNil(daily["resetAt"]),
		},
		"wallet": map[string]any{"balance": usageNum(nestedMap(freebucks, "wallet")["balance"], 0)},
	}
	// An unfunded account reports no balance at all; null keeps the header from
	// rendering "$0.00 available" as if it were a real number.
	if balance, ok := usageFiniteNum(freebucks["balance"]); ok {
		summary["balance"] = balance
	} else {
		summary["balance"] = nil
	}
	if monthly, ok := freebucks["monthly"].(map[string]any); ok {
		if remaining, ok := usageFiniteNum(monthly["remainingUsd"]); ok {
			entry := map[string]any{"remainingUsd": remaining, "resetAt": usageResetTimeToNil(monthly["resetAt"])}
			if limit, ok := usageFiniteNum(monthly["limitUsd"]); ok {
				entry["limitUsd"] = limit
			}
			summary["monthly"] = entry
		}
	}
	return summary
}
