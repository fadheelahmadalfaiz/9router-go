package dashboard

import (
	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/models"
	"9router/proxy/internal/providers"
	json "encoding/json/v2"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Batch provider testing, ported from upstream
// src/app/api/providers/test-batch/route.js.
//
// The three "Test All" buttons on the providers overview (upstream
// providers/page.js) called this endpoint; 9router-go shipped the buttons with
// an alert() placeholder, so a provider's connections were never actually
// probed from that page.
const (
	batchModeProvider   = "provider"
	batchModeOAuth      = "oauth"
	batchModeFree       = "free"
	batchModeAPIKey     = "apikey"
	batchModeCompatible = "compatible"
	batchModeAll        = "all"
)

var batchTestModes = []string{
	batchModeProvider, batchModeOAuth, batchModeFree,
	batchModeAPIKey, batchModeCompatible, batchModeAll,
}

type batchTestRequest struct {
	Mode string `json:"mode"`
	// ProviderID targets a single provider (mode "provider").
	ProviderID string `json:"providerId"`
	// ProviderIDs overrides the backend's own auth-group classification with
	// the exact set the caller is displaying. The overview page groups its cards
	// by catalog category, which is not the same partition as authType (a
	// freeTier provider holding an API key is filed under "API Key" on screen
	// but is not a noAuth provider upstream). Letting the caller name the set
	// makes the button test exactly the cards in front of the user instead of
	// whatever the backend guesses.
	ProviderIDs []string `json:"providerIds"`
}

type batchTestResult struct {
	Provider       string  `json:"provider"`
	ConnectionID   string  `json:"connectionId"`
	ConnectionName string  `json:"connectionName"`
	AuthType       string  `json:"authType"`
	AuthGroup      string  `json:"authGroup"`
	Valid          bool    `json:"valid"`
	LatencyMs      int64   `json:"latencyMs"`
	Refreshed      bool    `json:"refreshed"`
	Error          *string `json:"error"`
	TestedAt       string  `json:"testedAt"`
}

type batchTestSummary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

type batchTestResponse struct {
	Mode       string            `json:"mode"`
	ProviderID *string           `json:"providerId"`
	Results    []batchTestResult `json:"results"`
	Summary    batchTestSummary  `json:"summary"`
	TestedAt   string            `json:"testedAt"`
}

// connectionAuthGroup buckets a connection the way upstream's getAuthGroup
// does: a compatible endpoint is its own group, a registry noAuth provider is
// "free", and OAuth falls through to the registry check before landing in
// "oauth". Everything else counts as API key.
func connectionAuthGroup(provider, authType string) string {
	switch {
	case authType == "compatible":
		return batchModeCompatible
	case isNoAuthProvider(provider):
		return batchModeFree
	case authType == "oauth":
		return batchModeOAuth
	default:
		return batchModeAPIKey
	}
}

func isNoAuthProvider(provider string) bool {
	cfg, ok := providers.KnownProviders[provider]
	return ok && cfg.NoAuth
}

// connectionMatchesMode reports whether a connection belongs in the batch.
func connectionMatchesMode(conn *models.ProviderConnection, mode, providerID string, wanted []string) bool {
	if len(wanted) > 0 {
		return slices.ContainsFunc(wanted, func(id string) bool { return id == conn.Provider })
	}
	switch mode {
	case batchModeProvider:
		return providerID != "" && conn.Provider == providerID
	case batchModeAll:
		return true
	case batchModeCompatible:
		// authType is the reliable signal, but a node wired up before the
		// compatible authType existed would otherwise be skipped silently.
		return connectionAuthGroup(conn.Provider, conn.AuthType) == batchModeCompatible ||
			isCompatibleProviderID(conn.Provider)
	default:
		return connectionAuthGroup(conn.Provider, conn.AuthType) == mode
	}
}

// HandleTestBatch runs the connection probe across a group of connections.
//
// Probes run sequentially, not concurrently: two accounts of the same provider
// usually share an egress IP, and a burst of parallel probes is what got
// accounts rate-limited (and locked) in the first place. Sequential order is
// also what makes the per-connection latency in the response meaningful.
func (h *DashboardHandler) HandleTestBatch(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var req batchTestRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	}

	if req.Mode == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "mode is required")
		return
	}
	if !slices.Contains(batchTestModes, req.Mode) {
		handlerutil.WriteJSONError(w, http.StatusBadRequest,
			"invalid mode. Use: provider, oauth, free, apikey, compatible, all")
		return
	}
	if req.Mode == batchModeProvider && req.ProviderID == "" && len(req.ProviderIDs) == 0 {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "providerId is required for mode=provider")
		return
	}

	conns, err := h.Repo.GetProviderConnections("", true)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to load connections")
		return
	}

	results := make([]batchTestResult, 0, len(conns))
	for _, conn := range conns {
		if conn == nil || !connectionMatchesMode(conn, req.Mode, req.ProviderID, req.ProviderIDs) {
			continue
		}
		results = append(results, h.probeConnectionForBatch(r, conn))
	}

	resp := batchTestResponse{
		Mode:     req.Mode,
		Results:  results,
		Summary:  summarizeBatch(results),
		TestedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if req.ProviderID != "" {
		resp.ProviderID = &req.ProviderID
	}
	handlerutil.WriteJSON(w, http.StatusOK, resp)
}

func summarizeBatch(results []batchTestResult) batchTestSummary {
	summary := batchTestSummary{Total: len(results)}
	for _, result := range results {
		if result.Valid {
			summary.Passed++
		} else {
			summary.Failed++
		}
	}
	return summary
}

func (h *DashboardHandler) probeConnectionForBatch(r *http.Request, conn *models.ProviderConnection) batchTestResult {
	result := batchTestResult{
		Provider:       conn.Provider,
		ConnectionID:   conn.ID,
		ConnectionName: batchConnectionName(conn),
		AuthType:       conn.AuthType,
		AuthGroup:      connectionAuthGroup(conn.Provider, conn.AuthType),
		TestedAt:       time.Now().UTC().Format(time.RFC3339),
	}

	raw, _ := decodeConnectionData(conn.Data)
	if raw == nil {
		raw = map[string]any{}
	}
	data := parseConnectionProbeData(raw)

	started := time.Now()
	out := h.testSingleConnection(r.Context(), conn, data, raw)
	h.persistProbeResult(conn, raw, out)

	result.LatencyMs = time.Since(started).Milliseconds()
	result.Valid = out.valid
	result.Refreshed = out.refreshed
	if out.message != "" {
		message := out.message
		result.Error = &message
	}
	return result
}

func batchConnectionName(conn *models.ProviderConnection) string {
	if conn.Name != nil && *conn.Name != "" {
		return *conn.Name
	}
	if conn.Email != nil && *conn.Email != "" {
		return *conn.Email
	}
	return conn.Provider
}

// isCompatibleProviderID reports whether a provider id is a custom compatible
// node, matching upstream's prefix check.
func isCompatibleProviderID(providerID string) bool {
	return strings.HasPrefix(providerID, "openai-compatible") ||
		strings.HasPrefix(providerID, "anthropic-compatible")
}
