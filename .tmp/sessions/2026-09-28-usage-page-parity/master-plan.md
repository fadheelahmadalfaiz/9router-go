# Master Plan — `/dashboard/usage` parity dengan upstream decolua/9router

Status: **menunggu approval**
Tanggal: 2026-09-28
Baseline upstream: `E:\codingproject\vibecoding\9router` @ `page.js` + `UsageStats.js` + `usageRepo.js:getChartData`

## Tujuan

Tutup 5 gap yang teridentifikasi antara `/dashboard/usage` di 9router-go dan upstream:

1. State (tab, period, sortBy, sortOrder, viewMode) tidak masuk URL
2. Tab `logs` (`RequestLogger`) tidak ada
3. 3 chart tidak ada (`UsageChart`, `ProviderBarChart`, `TopModelsChart`)
4. Opsi period `All` tidak ada (backend sudah mendukung)
5. Tabel belum sortable + belum group/expandable

## Keputusan desain (sudah diverifikasi terhadap sumber)

### D1 — Chart: SVG sendiri, TANPA dependency baru

Upstream memakai `recharts`, yang **React-only**. Repo ini Svelte 5, jadi `recharts` tidak bisa dipakai. Kandidat lain (`layerchart`, `chart.js`) menambah dependency berat ke `bun.lock` (CI pakai `bun install --frozen-lockfile`, dan lockfile lokal saat ini versi 2 yang hanya bisa dibaca Bun ≥1.4 — berisikoVersions). Ketiga chart yang hilang sederhana (1 area + 2 bar), jadi diimplementasikan sebagai SVG murni dengan geometri di module pure yang dites.

**Konsekuensi:** `web/package.json` dan `bun.lock` tidak berubah sama sekali.

### D2 — Autentikasi endpoint baru

`SetupRoutes` (router.go:415) di-mount di bawah `middleware.RequireApiKey` yang **menolak session cookie** — inilah bug class yang sudah pernah menimpa `/api/cli-tools/all-statuses` (lihat CHANGELOG `[Unreleased]`). Route usage existing terdaftar di kedua group, padahal `SetupDashboardRoutes` (router.go:422, `RequireDashboardAuth`) sudah menerima session cookie **dan** API key **dan** CLI token.

**Keputusan:** endpoint baru didaftarkan **hanya** di `SetupDashboardRoutes`, mengikuti preseden fix CLI Tools. Diverifikasi lewat test auth (anon → 401, session → 200).

> Catatan: suspected shadowing route usage existing (terdaftar di dua group) akan dicek opportunistik lewat test. Bila memang ada, **melaporkan** — bukan dikerjakan diam-diam di luar scope.

### D3 — Pemisahan logic純 ke module `.ts`

Mengikuti pola yang sudah dipakai `quota/urlState.ts`: geometri chart, grouping tabel, filter provider, dan URL state semuanya pure function di module `.ts` bersebelahan, diuji dengan `bun test`. Ini juga menutup gap "tidak ada component-render test" yang disebut CHANGELOG.

### D4 — Format URL

```
/dashboard/usage
/dashboard/usage?tab=details
/dashboard/usage?period=7d&view=tokens
/dashboard/usage?tab=logs&sortBy=rawModel&sortOrder=asc
```

Default (`overview`, `today`, `rawModel`, `asc`, `costs`) di-omit. Param `sortBy`/`sortOrder`/`view` hanya ditulis saat tab `overview` aktif agar URL tidak berpenuh.

## Komponen (urutan eksekusi)

### C1 — Period `All` (kecil, fondasi C2)

| File                            | Perubahan                                                                             |
| ------------------------------- | ------------------------------------------------------------------------------------- |
| `web/src/components/analytics/types.ts` | `Period` += `'all'`; `PERIODS` += `{ value: 'all', label: 'All' }` (6 opsi,_layout `grid-cols-6`) |

Backend `usage_stats.go:193` sudah handle `period == "all"` (daysLimit 365) — **tidak ada perubahan Go**.

### C2 — URL state sync

| File                                       | Perubahan                                                                                                       |
| ------------------------------------------ | -------------------------------------------------------------------------------------------------------------- |
| `web/src/components/analytics/urlState.ts` **(baru)** | `parseUsageUrlState`, `buildUsageUrl`, `getUsageFilterSignature`, `USAGE_URL_DEFAULTS`. Pola mirror `quota/urlState.ts`. |
| `web/src/components/analytics/urlState.test.ts` **(baru)** | Table-driven: sanitasi, default omitted, param asing dipertahankan, idempoten.                    |
| `web/src/components/analytics/AnalyticsView.svelte` | Hydrate saat init (sebelum `onMount`), `$effect` tulis `pushState` untuk filter / `replaceState` untuk tab, `popstate` restore, guard `pathToTab() === 'analytics'`. |

### C3 — Perbaikan filter provider topology

| File                                          | Perubahan                                                                                                                              |
| --------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `web/src/components/analytics/topology.ts` **(baru)** | `isLLMProvider(id)` (port `UsageStats.js:8-12`, pakai `serviceKinds`), `getFreeLLMProviderIds()` (dari `noAuth`), `buildTopologyProviders()` — **menghapus** hardcoded `FREE_DEFAULTS` di `AnalyticsView.svelte:298`. |
| `web/src/components/analytics/topology.test.ts` **(baru)** | Test: provider non-LLM (`serviceKinds: ['tts']`) ditolak, noAuth LLM masuk, alias ter-resolve.                                    |
| `web/src/components/analytics/AnalyticsView.svelte` | `topologyProviders` jadi `$derived` yang memanggil fungsi pure; hapus array hardcoded.                                                  |

### C4 — Tabel sortable + group/expand

| File                                            | Perubahan                                                                                                                                 |
| ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| `web/src/components/analytics/tableModel.ts` **(baru)** | `sortUsageData`, `groupUsageData`, `getGroupKey`, `computeCostSplit` (input/cached/output share — port `UsageStats.js:96-104`), `buildPendingMaps` (dari `PendingState{byModel,byAccount}`), `getTableConfig(view)` (4 column-set + `storageKey`). |
| `web/src/components/analytics/tableModel.test.ts` **(baru)** | Test sorting string-vs-number, grouping + summary, cost split, pending map.                                                                |
| `web/src/components/analytics/UsageBreakdownTable.svelte` | Tulis ulang: group summary row + chevron expand, expand state di localStorage (`usage-stats:expanded-*`), header sortable, cost-split columns, highlight `pending > 0`, hapus sort hardcoded `requests` desc. |

### C5 — Backend: `/api/usage/chart` + `/api/usage/request-logs`

| File                                        | Perubahan                                                                                                                             |
| ------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/db/usage.go`                      | Tambah `UsageDailyRow{DateKey, Data}` + `GetUsageDailyWithKeys(limit)` (ASC, dengan `dateKey` — `GetUsageDailyRecent` tidak punya key). Additive. |
| `internal/handlers/usage_chart.go` **(baru)** | `HandleUsageChart` — port `getChartData`: `today`/`24h` → 24 bucket jam; `7d`/`30d`/`60d` → N bucket hari; `all` → hari dari `dateKey` tertua. Response `[]{label,tokens,cost,requests}`. |
| `internal/handlers/usage_requestlogs.go` **(baru)** | `HandleUsageRequestLogs` — port `getRecentLogs`, pipe-delimited string.                                                               |
| `internal/handlers/usage_chart_test.go` **(baru)** | Table-driven + sqlite `:memory:`: bucketing per period, validasi period, label, empty state.                                          |
| `internal/handlers/usage_requestlogs_test.go` **(baru)** | Format string, masking account, limit.                                                                                                |
| `internal/handlers/router.go`               | Registrasi 2 route di `SetupDashboardRoutes` saja (D2).                                                                                 |
| `internal/handlers/router_test.go`           | Test auth: anonymous → 401, session → 200 (copy pola `TestSetupServerRouter_CLIToolsStatusIsADashboardRead`).                            |

### C6 — 3 chart (SVG, tanpa dependency)

| File                                              | Perubahan                                                                                             |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| `web/src/components/analytics/charts/chartModel.ts` **(baru)** | `fmtTokens`/`fmtCost`, `niceMax`, `buildLinePath` (area), `buildBars`, `ticksFor`. Geometri pure → **dihitung di TS, bukan di markup**. |
| `web/src/components/analytics/charts/chartModel.test.ts` **(baru)** | Test skala/poros/path/format.                                                                           |
| `web/src/components/analytics/charts/ChartFrame.svelte` **(baru)** | Shell: card + header + slot + state empty/loading + tooltip positioning.                                  |
| `web/src/components/analytics/charts/UsageChart.svelte` **(baru)** | Area chart, 3 mode (Tokens `#6366f1` / Requests `#14b8a6` / Cost `#f59e0b`), tinggi 220, gradient fill,_fetch `/api/usage/chart?period=`. |
| `web/src/components/analytics/charts/ProviderBarChart.svelte` **(baru)** | Bar vertikal, 2 mode, tinggi 180, 8 warna, label dipotong 10 char.                                      |
| `web/src/components/analytics/charts/TopModelsChart.svelte` **(baru)** | Bar horizontal, top 5, 2 mode, tinggi 180, 5 warna, label dipotong 22 char.                             |
| `web/src/api/client.ts`                          | Tambah `getUsageChart(period)` + `getUsageRequestLogs()`.                                                                       |
| `web/src/components/analytics/AnalyticsView.svelte` | `<UsageChart {period}>` setelah topology; grid `lg:grid-cols-2` berisi `ProviderBarChart` + `TopModelsChart` (render hanya bila `byProvider`/`byModel` ada, seperti upstream). |

### C7 — Tab `logs`

| File                                                    | Perubahan                                                                                       |
| ------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `web/src/components/analytics/RequestLogsView.svelte` **(baru)** | Port `RequestLogger.js`: poll 3s + toggle auto-refresh, tabel DateTime/Model/Provider/Account/In/Out/Status, parse string pipe, status `PENDING`/`FAILED`/`OK` → warna. |
| `web/src/components/analytics/AnalyticsView.svelte`     | Tambah tombol tab `Logs` (3 tombol) + blok render `{activeTab === 'logs'}`.                        |

> Catatan: upstream hanya Reach `logs` lewat `?tab=logs` (SegmentedControl-nya 2 opsi). Di sini tombolnya **visible**, sesuai "no dead ends / no dead code".

## Aturan yang dipatuhi

- `AGENTS.md` §4: file ≤ 300 LoC, complexity ≤ 15, `%w` error wrap, tanpa `panic`
- `AGENTS.md` §5: test table-driven stdlib `testing.T`, sqlite `:memory:`
- `AGENTS.md` §6: Svelte 5 runes, `onclick` (bukan `on:click`), zero `export let`, zero `<slot />`
- `AGENTS.md` §3: tidak ada hardcoded provider list (C3 menghapus `FREE_DEFAULTS`)
- Token desain dari `web/src/index.css` (`bg-surface`, `text-text-muted`, `text-brand-500`, `rounded-[14px]`, `border-border-subtle`)

## Verifikasi (setiap komponen, incremental)

```bash
rtk go test ./internal/...            # C5
cd web && bun test                    # C2, C3, C4, C6
cd web && bun run build               # tsc -b + vite build
cd web && bun run lint                # oxlint
```

Setiap langkah: **implement → validate → lanjut**. Kalau ada failure: report, usulkan fix, tunggu approval — tidak auto-fix.

## Di luar scope (dilaporkan, tidak dikerjakan)

- Route usage existing terdaftar di `SetupRoutes` **dan** `SetupDashboardRoutes` → suspected chi shadowing. Dicek lewat test C5; bila terbukti bug, dilaporkan terpisah.
- `RecentRequests` upstream re-tick tiap 1s (`TimeAgo`); Go polling 5s. Presisi beda ~5s, bukan parity break.
- Fallback `accountName` dari `connectionId` untuk tabel (Go sudah punya di `byAccount`, tapi `UsageBreakdownTable` belum memprioritaskannya) —BAKAL dikerjakan di C4 sekalian.

## Estimasi

| Komponen | File baru | File diubah | Risiko |
| -------- | --------- | ----------- | ------ |
| C1       | 0         | 1           | sangat rendah |
| C2       | 2         | 1           | rendah (pola sudah terbukti di quota) |
| C3       | 2         | 1           | rendah |
| C4       | 2         | 1           | sedang (tabel rewrite) |
| C5       | 3         | 3           | sedang (SQL + auth) |
| C6       | 5         | 2           | sedang (SVG geometry) |
| C7       | 1         | 1           | rendah |
