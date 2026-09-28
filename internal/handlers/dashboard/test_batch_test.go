package dashboard

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/models"
)

// insertBatchConnection creates an active connection. The create helper takes
// authType directly, so a test can pin the exact auth group it needs.
func insertBatchConnection(t *testing.T, repo *db.Repo, id, provider, authType string) {
	t.Helper()
	if err := repo.CreateProviderConnectionFull(id, provider, authType, id, nil, "{}"); err != nil {
		t.Fatalf("create connection %s: %v", id, err)
	}
}

func postBatchTest(t *testing.T, repo *db.Repo, body string) (int, batchTestResponse) {
	t.Helper()
	router := setupTestRouter(repo)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/test-batch", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var resp batchTestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

// TestConnectionAuthGroup pins the auth-group partition. It is the whole point
// of the endpoint: "Test All" on the Free Tier section must probe the free
// connections, not the API-key ones.
func TestConnectionAuthGroup(t *testing.T) {
	tests := []struct {
		keyName   string
		provider  string
		authType  string
		wantGroup string
	}{
		{keyName: "oauth lands in oauth", provider: "claude", authType: "oauth", wantGroup: batchModeOAuth},
		{keyName: "api key lands in apikey", provider: "openai", authType: "apikey", wantGroup: batchModeAPIKey},
		{keyName: "cookie lands in apikey", provider: "chatgpt-web", authType: "cookie", wantGroup: batchModeAPIKey},
		{keyName: "compatible is its own group", provider: "my-node", authType: "compatible", wantGroup: batchModeCompatible},
		{keyName: "unknown provider defaults to apikey", provider: "mystery", authType: "", wantGroup: batchModeAPIKey},
		{keyName: "noAuth registry provider is free", provider: "opencode", authType: "apikey", wantGroup: batchModeFree},
	}

	for _, tt := range tests {
		t.Run(tt.keyName, func(t *testing.T) {
			if got := connectionAuthGroup(tt.provider, tt.authType); got != tt.wantGroup {
				t.Errorf("connectionAuthGroup(%q, %q) = %q, want %q", tt.provider, tt.authType, got, tt.wantGroup)
			}
		})
	}
}

// TestHandleTestBatch_RejectsBadRequests covers the input contract: mode is
// required, unknown modes are refused, and mode=provider without a provider is
// a client error rather than a silent empty result.
func TestHandleTestBatch_RejectsBadRequests(t *testing.T) {
	repo, cleanup := setupProbeTestDB(t)
	defer cleanup()

	tests := []struct {
		keyName    string
		body       string
		wantStatus int
	}{
		{keyName: "missing mode", body: `{}`, wantStatus: http.StatusBadRequest},
		{keyName: "unknown mode", body: `{"mode":"nonsense"}`, wantStatus: http.StatusBadRequest},
		{keyName: "provider mode without id", body: `{"mode":"provider"}`, wantStatus: http.StatusBadRequest},
		{keyName: "malformed json", body: `{`, wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.keyName, func(t *testing.T) {
			status, _ := postBatchTest(t, repo, tt.body)
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
		})
	}
}

// TestHandleTestBatch_EmptySelectionAnswersZero — a group with nothing in it
// must report total 0, not null results the UI would fail to map.
func TestHandleTestBatch_EmptySelectionAnswersZero(t *testing.T) {
	repo, cleanup := setupProbeTestDB(t)
	defer cleanup()

	status, resp := postBatchTest(t, repo, `{"mode":"oauth"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if resp.Summary.Total != 0 || resp.Summary.Passed != 0 || resp.Summary.Failed != 0 {
		t.Errorf("summary = %+v, want all zero", resp.Summary)
	}
	if resp.Results == nil {
		t.Error("results must be an empty array, not null")
	}
}

// TestHandleTestBatch_SelectsByModeAndProvider — mode selects the auth group,
// mode=provider selects one provider, and providerIds overrides both. The
// override exists because the overview page groups cards by catalog category,
// which is a different partition than authType.
func TestHandleTestBatch_SelectsByModeAndProvider(t *testing.T) {
	repo, cleanup := setupProbeTestDB(t)
	defer cleanup()

	insertBatchConnection(t, repo, "conn-oauth", "claude", "oauth")
	insertBatchConnection(t, repo, "conn-apikey", "openai", "apikey")
	insertBatchConnection(t, repo, "conn-free", "opencode", "apikey")

	// A turned-off connection must never be probed, whatever its group.
	insertBatchConnection(t, repo, "conn-off", "claude", "oauth")
	if _, err := repo.RawDB().Exec(`UPDATE providerConnections SET isActive = 0 WHERE id = 'conn-off'`); err != nil {
		t.Fatalf("disable connection: %v", err)
	}

	stubProbeDo(t, func(string, string) (int, []byte) { return http.StatusOK, []byte(`{}`) })

	tests := []struct {
		keyName     string
		body        string
		wantIDs     []string
		wantTotal   int
		wantSkipped bool
	}{
		{
			keyName:   "oauth mode",
			body:      `{"mode":"oauth"}`,
			wantIDs:   []string{"conn-oauth"},
			wantTotal: 1,
		},
		{
			keyName:   "apikey mode",
			body:      `{"mode":"apikey"}`,
			wantIDs:   []string{"conn-apikey"},
			wantTotal: 1,
		},
		{
			keyName:   "free mode",
			body:      `{"mode":"free"}`,
			wantIDs:   []string{"conn-free"},
			wantTotal: 1,
		},
		{
			keyName:   "all mode includes every auth group",
			body:      `{"mode":"all"}`,
			wantIDs:   []string{"conn-oauth", "conn-apikey", "conn-free"},
			wantTotal: 3,
		},
		{
			keyName:   "provider mode",
			body:      `{"mode":"provider","providerId":"claude"}`,
			wantIDs:   []string{"conn-oauth"},
			wantTotal: 1,
		},
		{
			keyName:   "providerIds override wins over the group",
			body:      `{"mode":"oauth","providerIds":["openai","opencode"]}`,
			wantIDs:   []string{"conn-apikey", "conn-free"},
			wantTotal: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.keyName, func(t *testing.T) {
			status, resp := postBatchTest(t, repo, tt.body)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200", status)
			}
			if resp.Summary.Total != tt.wantTotal {
				t.Fatalf("total = %d, want %d (results %+v)", resp.Summary.Total, tt.wantTotal, resp.Results)
			}
			got := make([]string, 0, len(resp.Results))
			for _, result := range resp.Results {
				got = append(got, result.ConnectionID)
			}
			for _, want := range tt.wantIDs {
				if !contains(got, want) {
					t.Errorf("results %v missing %q", got, want)
				}
			}
			if contains(got, "conn-off") {
				t.Error("a disabled connection must never be probed")
			}
		})
	}
}

// TestHandleTestBatch_SummarisesPassAndFail — a failing probe must count as
// failed and carry its message, which is what the overview renders per row.
func TestHandleTestBatch_SummarisesPassAndFail(t *testing.T) {
	repo, cleanup := setupProbeTestDB(t)
	defer cleanup()

	insertBatchConnection(t, repo, "conn-good", "opencode", "apikey")
	insertBatchConnection(t, repo, "conn-bad", "opencode", "apikey")

	// opencode is a noAuth provider probed with the public key, so the stub
	// decides the outcome: 200 for one, 500 for the other.
	var calls int
	stubProbeDo(t, func(string, string) (int, []byte) {
		calls++
		if calls == 1 {
			return http.StatusOK, []byte(`{}`)
		}
		return http.StatusInternalServerError, []byte(`{"error":"upstream exploded"}`)
	})

	status, resp := postBatchTest(t, repo, `{"mode":"free"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if resp.Summary.Total != 2 {
		t.Fatalf("total = %d, want 2", resp.Summary.Total)
	}
	if resp.Summary.Passed+resp.Summary.Failed != resp.Summary.Total {
		t.Errorf("passed+failed = %d, want total %d", resp.Summary.Passed+resp.Summary.Failed, resp.Summary.Total)
	}

	var failed int
	for _, result := range resp.Results {
		if !result.Valid {
			failed++
			if result.Error == nil {
				t.Error("a failed result must carry an error message")
			}
		}
	}
	if failed != resp.Summary.Failed {
		t.Errorf("failed rows = %d, summary says %d", failed, resp.Summary.Failed)
	}
}

// TestBatchConnectionName_FallsBackThroughNameEmailProvider — the list shows
// whatever a user recognises the account by, and never an empty cell.
func TestBatchConnectionName_FallsBackThroughNameEmailProvider(t *testing.T) {
	name := "Work"
	email := "a@b.c"

	tests := []struct {
		keyName string
		conn    models.ProviderConnection
		want    string
	}{
		{keyName: "prefers name", conn: models.ProviderConnection{Provider: "claude", Name: &name, Email: &email}, want: "Work"},
		{keyName: "falls back to email", conn: models.ProviderConnection{Provider: "claude", Email: &email}, want: "a@b.c"},
		{keyName: "falls back to provider", conn: models.ProviderConnection{Provider: "claude"}, want: "claude"},
		{keyName: "ignores empty name", conn: models.ProviderConnection{Provider: "claude", Name: new(string)}, want: "claude"},
	}

	for _, tt := range tests {
		t.Run(tt.keyName, func(t *testing.T) {
			if got := batchConnectionName(&tt.conn); got != tt.want {
				t.Errorf("batchConnectionName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsCompatibleProviderID(t *testing.T) {
	tests := []struct {
		providerID string
		want       bool
	}{
		{providerID: "openai-compatible-abc", want: true},
		{providerID: "anthropic-compatible-xyz", want: true},
		{providerID: "openai-compatible", want: true},
		{providerID: "claude", want: false},
		{providerID: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.providerID, func(t *testing.T) {
			if got := isCompatibleProviderID(tt.providerID); got != tt.want {
				t.Errorf("isCompatibleProviderID(%q) = %v, want %v", tt.providerID, got, tt.want)
			}
		})
	}
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
