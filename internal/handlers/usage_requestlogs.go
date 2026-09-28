package handlers

import (
	"9router/proxy/internal/db"
	"9router/proxy/internal/handlerutil"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	usageRequestLogDefaultLimit = 200
	usageRequestLogMaxLimit     = 1000
)

// HandleUsageRequestLogs renders recent request history as pipe-delimited rows,
// matching upstream /api/usage/request-logs
// (src/lib/db/repos/usageRepo.js getRecentLogs) so the dashboard log table can
// port without an adapter:
//
//	DD-MM-YYYY HH:MM:SS | model | PROVIDER | account | in | out | STATUS
//
// The delimiter is upstream's own contract, not a new invention: the log table
// splits on " | " and skips any row that does not carry every field, so a model
// or account containing the separator degrades to a dropped row rather than a
// shifted column.
func HandleUsageRequestLogs(repo *db.Repo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := usageRequestLogDefaultLimit
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= usageRequestLogMaxLimit {
				limit = parsed
			}
		}

		logs := buildUsageRequestLogs(repo, limit)
		handlerutil.WriteJSON(w, http.StatusOK, logs)
	}
}

func buildUsageRequestLogs(repo *db.Repo, limit int) []string {
	rows, err := repo.GetRecentUsageHistory(limit)
	if err != nil {
		return []string{}
	}

	accountByConnection := make(map[string]string, len(rows))
	if connections, err := repo.GetProviderConnections("", true); err == nil {
		for _, connection := range connections {
			name := connection.ID
			if connection.Name != nil && *connection.Name != "" {
				name = *connection.Name
			} else if connection.Email != nil && *connection.Email != "" {
				name = *connection.Email
			}
			accountByConnection[connection.ID] = name
		}
	}

	logs := make([]string, 0, len(rows))
	for _, row := range rows {
		account := "-"
		if named, ok := accountByConnection[row.ConnectionID]; ok && named != "" {
			account = named
		} else if row.ConnectionID != "" {
			account = shortConnectionID(row.ConnectionID)
		}

		provider := "-"
		if row.Provider != "" {
			provider = strings.ToUpper(row.Provider)
		}
		model := "-"
		if row.Model != "" {
			model = row.Model
		}
		status := "-"
		if row.Status != "" {
			status = row.Status
		}

		logs = append(logs, strings.Join([]string{
			formatUsageLogDate(row.Timestamp),
			model,
			provider,
			account,
			strconv.Itoa(row.PromptTokens),
			strconv.Itoa(row.CompletionTokens),
			status,
		}, " | "))
	}
	return logs
}

func shortConnectionID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// formatUsageLogDate renders DD-MM-YYYY HH:MM:SS in local time, the format the
// upstream log table expects. An unparseable timestamp falls back to the raw
// value so the row still renders rather than disappearing.
func formatUsageLogDate(timestamp string) string {
	parsed, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return timestamp
	}
	local := parsed.Local()
	return fmt.Sprintf("%02d-%02d-%04d %02d:%02d:%02d",
		local.Day(), int(local.Month()), local.Year(),
		local.Hour(), local.Minute(), local.Second())
}
