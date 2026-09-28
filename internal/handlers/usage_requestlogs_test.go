package handlers

import (
	"9router/proxy/internal/db"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func seedUsageLog(t *testing.T, repo *db.Repo, provider, model, connectionID, apiKey, status string, prompt, completion int) {
	t.Helper()
	tokens := `{"prompt_tokens":` + strconv.Itoa(prompt) + `,"completion_tokens":` + strconv.Itoa(completion) + `}`
	if err := repo.InsertUsageHistory(
		provider, model, connectionID, apiKey, "/v1/messages",
		prompt, completion, 0.1, status, prompt+completion, "{}", tokens,
	); err != nil {
		t.Fatalf("insert usage history: %v", err)
	}
}

func getLogs(t *testing.T, repo *db.Repo, query string) ([]string, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/usage/request-logs"+query, nil)
	HandleUsageRequestLogs(repo)(rec, req)

	if rec.Code != http.StatusOK {
		return nil, rec.Code
	}
	var logs []string
	if err := json.Unmarshal(rec.Body.Bytes(), &logs); err != nil {
		t.Fatalf("decode log payload: %v (body %s)", err, rec.Body.String())
	}
	return logs, rec.Code
}

// TestHandleUsageRequestLogs_RowFormat — the row is the pipe-delimited contract
// the dashboard log table splits on, with the provider upper-cased and the
// timestamp in DD-MM-YYYY HH:MM:SS.
func TestHandleUsageRequestLogs_RowFormat(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	seedUsageLog(t, repo, "claude", "claude-sonnet-4-5", "", "sk-test", "success", 1200, 340)

	logs, code := getLogs(t, repo, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(logs) != 1 {
		t.Fatalf("log count = %d, want 1", len(logs))
	}

	parts := strings.Split(logs[0], " | ")
	if len(parts) != 7 {
		t.Fatalf("field count = %d, want 7 (row %q)", len(parts), logs[0])
	}
	if parts[1] != "claude-sonnet-4-5" {
		t.Errorf("model = %q", parts[1])
	}
	if parts[2] != "CLAUDE" {
		t.Errorf("provider = %q, want upper-cased", parts[2])
	}
	if parts[3] != "-" {
		t.Errorf("account = %q, want %q for an unmapped connection", parts[3], "-")
	}
	if parts[4] != "1200" || parts[5] != "340" {
		t.Errorf("tokens = %q / %q, want 1200 / 340", parts[4], parts[5])
	}
	if parts[6] != "success" {
		t.Errorf("status = %q", parts[6])
	}
	// DD-MM-YYYY HH:MM:SS
	if len(parts[0]) != 19 || parts[0][2] != '-' || parts[0][10] != ' ' {
		t.Errorf("date %q is not DD-MM-YYYY HH:MM:SS", parts[0])
	}
}

// TestHandleUsageRequestLogs_MissingFieldsUseDash — upstream renders a dash for
// every blank field rather than an empty cell.
func TestHandleUsageRequestLogs_MissingFieldsUseDash(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	seedUsageLog(t, repo, "", "", "", "", "", 0, 0)

	logs, code := getLogs(t, repo, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	parts := strings.Split(logs[0], " | ")
	if parts[1] != "-" || parts[2] != "-" || parts[3] != "-" {
		t.Errorf("blank fields = %q, want dashes", logs[0])
	}
}

// TestHandleUsageRequestLogs_LimitIsClamped — a bad or hostile limit falls back
// to the default instead of being trusted.
func TestHandleUsageRequestLogs_LimitIsClamped(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	seedUsageLog(t, repo, "claude", "m", "", "sk", "success", 1, 1)

	tests := []struct {
		keyName string
		query   string
	}{
		{keyName: "default", query: ""},
		{keyName: "valid limit", query: "?limit=5"},
		{keyName: "zero falls back", query: "?limit=0"},
		{keyName: "negative falls back", query: "?limit=-3"},
		{keyName: "non-numeric falls back", query: "?limit=abc"},
		{keyName: "above the cap falls back", query: "?limit=99999"},
	}

	for _, tt := range tests {
		t.Run(tt.keyName, func(t *testing.T) {
			logs, code := getLogs(t, repo, tt.query)
			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}
			if len(logs) != 1 {
				t.Fatalf("log count = %d, want 1", len(logs))
			}
		})
	}
}

// TestHandleUsageRequestLogs_EmptyDatabase — an empty history must yield an
// empty array, not a null body the table would fail to map.
func TestHandleUsageRequestLogs_EmptyDatabase(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	logs, code := getLogs(t, repo, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(logs) != 0 {
		t.Fatalf("log count = %d, want 0", len(logs))
	}
}

// TestFormatUsageLogDate — an unparseable timestamp falls back to the raw value
// so the row still renders instead of vanishing from the table.
func TestFormatUsageLogDate(t *testing.T) {
	if got := formatUsageLogDate("not-a-timestamp"); got != "not-a-timestamp" {
		t.Errorf("unparseable timestamp = %q, want the raw value", got)
	}
	if got := formatUsageLogDate(""); got != "" {
		t.Errorf("empty timestamp = %q, want empty", got)
	}
	if got := formatUsageLogDate("2026-09-28T10:11:12Z"); len(got) != 19 {
		t.Errorf("formatted length = %d, want 19 (%q)", len(got), got)
	}
}
