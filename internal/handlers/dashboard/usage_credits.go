package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// iFlow and Vercel AI Gateway — the two handlers upstream keeps in
// open-sse/services/usage/misc.js.
//
// iFlow has no quota endpoint at all: upstream's handler makes no request and
// returns a fixed message. The case exists so the dashboard says
// "usage tracked per request" instead of "usage API not implemented", which
// would read as a broken integration.

var vercelCreditsURL = "https://ai-gateway.vercel.sh/v1/credits"

// vercelMonthlyCreditUSD is the free monthly allocation Vercel grants on the
// AI Gateway. The credits endpoint reports only balance and total_used, so the
// denominator has to come from somewhere — upstream uses the same constant.
const vercelMonthlyCreditUSD = 5.0

func fetchIflowUsage(_ context.Context) usageResult {
	return usageResult{message: "iFlow connected. Usage tracked per request.", bare: true}
}

// fetchVercelCredits surfaces a credit balance as one "Remaining (USD)" row
// plus an "Unlimited" spend row, because there is no spending cap to draw a
// percentage against.
func fetchVercelCredits(ctx context.Context, apiKey string) usageResult {
	if strings.TrimSpace(apiKey) == "" {
		return usageResult{message: "Vercel AI Gateway API key not available.", bare: true}
	}

	status, _, out, err := usageGet(ctx, vercelCreditsURL, map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Accept":        "application/json",
	})
	if err != nil {
		return usageResult{message: "Vercel AI Gateway error: " + err.Error(), bare: true}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return usageResult{message: "Vercel AI Gateway API key invalid or expired.", bare: true}
	}
	if status < 200 || status >= 300 {
		return usageResult{
			message: fmt.Sprintf("Vercel AI Gateway credits API error (%s)", usageErrorSnippet(out, status)),
			bare:    true,
		}
	}

	data := usageJSON(out)
	if data == nil {
		return usageResult{message: "Vercel AI Gateway credits response was not JSON.", bare: true}
	}
	// Vercel returns decimal strings; usageNum parses them.
	balance := usageNum(data["balance"], 0)
	totalUsed := usageNum(data["total_used"], 0)
	if balance <= 0 && totalUsed <= 0 {
		return usageResult{
			plan:    "Pay-as-you-go",
			message: "Vercel AI Gateway connected. No credit allocation found (BYOK or unfunded account).",
			bare:    true,
		}
	}

	return usageResult{
		plan: "Pay-as-you-go",
		quotas: map[string]any{
			"Used (USD)": map[string]any{
				"used":                totalUsed,
				"total":               float64(0),
				"remaining":           float64(0),
				"remainingPercentage": float64(100),
				"unlimited":           true,
			},
			"Remaining (USD)": map[string]any{
				"used":                balance,
				"total":               vercelMonthlyCreditUSD,
				"remaining":           balance,
				"remainingPercentage": balance / vercelMonthlyCreditUSD * 100,
				"unlimited":           false,
			},
		},
	}
}
