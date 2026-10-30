# Master Plan — MiniMax quota tracker parity + hapus bar fake

## Diagnosis (evidence)

| Gejala | Root cause |
|---|---|
| Image 1: semua kartu "Account active. No quota limits tracked." | `fetchProviderUsage()` (`internal/handlers/dashboard/usage_providers.go:69`) **tidak punya case `minimax` / `minimax-cn`**. Dispatch jatuh ke default → `HandleGetConnectionUsage` (`usage.go:125`) balas `{plan, quotas:{}}`.nolimit → dashboard render empty state. |
| Image 2: bar `0 / ∞`, `0%`, garis merah tipis, tidak ada grafik | Fallback `modelLock_*` di `usage.go:111-122` memalsukan baris quota `{used:0, total:0, remainingPercentage:0, resetAt:<lock>}`. `QuotaTable.svelte:179` render `total<=0` → `∞`, `getRemainingPercentage` → `0` → bar width 0%. Data ini **palsu** (bukan kuota upstream). |
| Image 3 (upstream OK) | Upstream punya `open-sse/services/usage/minimax.js` yang memanggil `token_plan/remains` / `coding_plan/remains` dan menghasilkan `M-series (5h|7d)`, `Video (5h|7d)` dengan `used/total/remainingPercentage` asli. Upstream route **tidak punya** fallback `modelLock_` sama sekali. |

## Components (urutan eksekusi)

### 1. `internal/handlers/dashboard/usage_minimax.go` (BARU)
Port `open-sse/services/usage/minimax.js` → `fetchMiniMaxUsage(ctx, apiKey, provider) usageResult`.

- `miniMaxUsageURLs` sebagai `var` (bisa di-override test):
  - `minimax`: `https://www.minimax.io/v1/token_plan/remains`, `https://api.minimax.io/v1/api/openplatform/coding_plan/remains`
  - `minimax-cn`: `https://www.minimaxi.com/v1/api/openplatform/coding_plan/remains`, `https://api.minimaxi.com/v1/api/openplatform/coding_plan/remains`
- `countMeansRemaining = strings.Contains(url, "/coding_plan/remains")`
- Auth-like detection → `MiniMax API key invalid or inactive. Use an active Token/Coding Plan key.` (401/403 / `base_resp.status_code == 1004` / regex `token plan|coding plan|invalid api key|invalid key|unauthorized|inactive`)
- Non-2xx + 404/405/5xx → fallback ke URL berikutnya; selain itu `MiniMax connected. Usage endpoint error (N)`
- Parse `model_remains` / `modelRemains`; filter `hasMiniMaxQuota`
- `formatMiniMaxQuotaName`: `MiniMax-M*` & `general` → `M-series`; title-case + fix `To`/`Tts`/`Hd`
- `addMiniMaxQuota` untuk `(5h)` dan `(7d)`: kalau `total<=0` tapi ada `*_remaining_percent` → normalisasi `total=100` dan count sintetis (dihitung sesuai `countMeansRemaining`)
- `resetAt` dari `remains_time` (ms relatif ke now) atau `end_time`
- Pesan kosong → `MiniMax connected. No quota data was returned.` / `MiniMax connected. Unable to extract quota usage.`
- Reuse: `usageGet`, `usageJSON`, `usageNum`, `usageFiniteNum`, `usageResetTime`

### 2. Wire dispatch (`usage_providers.go`)
Tambah `case "minimax", "minimax-cn": return fetchMiniMaxUsage(ctx, firstNonEmptyStr(apiKey, accessToken), provider), true`

### 3. Hapus baris kuota palsu dari fallback lock (`usage.go`)
Opsi A (REKOMENDASI, parity penuh dgn upstream): hapus blok `modelLock_*` dari response usage; provider tanpa fetcher balas `{message: "Usage API not implemented for <provider>"}` sehingga kartu menampilkan alasan jelas, bukan `0 / ∞`.
Opsi B (konservatif): tetap kirim baris lock tapi **skip lock yang sudah kedaluwarsa** dan set `remainingPercentage`/bar sesuai status lock (tetap tidak presisi).

> Catatan: dampaknya sama untuk provider lain yang belum punya fetcher (claude, github, gemini-cli, kimi, glm, zed, freebuff, vercel-a-gateway) — semuanya hari ini menampilkan bar `0 / ∞` palsu. Lock tetap看得 di ProviderDetailView + routing health.

### 4. Test `internal/handlers/dashboard/usage_minimax_test.go`
Table-driven `testing.T` + `httptest.NewServer`, override `miniMaxUsageURLs`:
- quota count-based (total>0) → used/total/percentage benar
- quota percent-only M-series → total=100, percentage dari upstream
- `countMeansRemaining` flip pada `coding_plan/remains`
- 401 → pesan invalid key
- 404 pada URL pertama → fallback ke URL kedua
- `MiniMax-M*` → `M-series (5h)` / `(7d)`
- dispatch test: `fetchProviderUsage(ctx, "minimax", ...)` ok=true

### 5. Verifikasi
`go build ./... && go test ./internal/handlers/dashboard/...` + `gofmt`

### 6. `CHANGELOG.md` entry `[Unreleased]` + parity note upstream

Out of scope (dicegat user dulu): fetcher live untuk claude / github / gemini-cli / kimi / glm / zed / freebuff.
