# Auto-Sync Fork: `fadheelahmadalfaiz/9router-go`

Fork ini mengikuti upstream [`luqman-v1/9router-go`](https://github.com/luqman-v1/9router-go) secara otomatis.

## Workflow: `Sync Upstream`

File: `.github/workflows/sync-upstream.yml`

### Trigger

| Trigger         | Jadwal / Cara                                   |
| --------------- | ----------------------------------------------- |
| `schedule`      | Setiap jam, menit ke-7 (`cron: "7 * * * *"`)     |
| `workflow_dispatch` | Manual dari tab **Actions → Sync Upstream → Run workflow** |

### Yang Dilakukan

1. Checkout `main` dengan `fetch-depth: 0` (butuh full history supaya merge bukan squash).
2. Tambah remote `upstream` → `https://github.com/luqman-v1/9router-go.git`, fetch branch `main`.
3. Hitung divergensi: `behind` / `ahead`.
4. Kalau `behind > 0`, merge dengan `-X theirs` (upstream menang saat konflik).
5. Push hasil merge ke `origin/main`.
6. Tulis ringkasan ke job summary.

Kalau fork sudah paling baru, workflow hanya melaporkan "already up to date" tanpa commit.

### Merge Strategy

`-X theirs` → **upstream selalu menang saat konflik.**

Fork ini sengaja tidak menyimpan patch lokal. Kalau nanti kamu menambah custom change, konflik akan muncul dan upstream akan menimpa. Solusinya: simpan patch di branch terpisah, bukan di `main`.

### Runbook

**Trigger manual:**
```bash
gh workflow run sync-upstream.yml --repo fadheelahmadalfaiz/9router-go --ref main
```

**Lihat status:**
```bash
gh run list --repo fadheelahmadalfaiz/9router-go --workflow sync-upstream.yml
```

**Matikan auto-sync** (edit `sync-upstream.yml`, hapus blok `schedule`):
```yaml
on:
  # schedule:
  #   - cron: "7 * * * *"
  workflow_dispatch:
```

### Dampak ke Deployment

Easypanel `app_9router-go` di VPS2 dikonfigurasi:

```json
{
  "source": { "type": "github", "owner": "fadheelahmadalfaiz", "repo": "9router-go", "ref": "main", "autoDeploy": true },
  "build":  { "type": "dockerfile" }
}
```

Artinya: **setiap push ke `main` (manual atau dari sync) memicu rebuild image + deploy otomatis.**

Data persisten di-bind ke host, jadi deploy tidak menghapus data:
- Host: `/etc/easypanel/projects/app/9router-go/data`
- Container: `/root/.9router`

### Rantai Workflow di Fork

```
luqman-v1/9router-go  (upstream, sumber perubahan)
        │
        │  schedule tiap jam :07
        ▼
Sync Upstream  ──merge -X theirs──►  main di fork
        │
        │  push ke main
        ▼
CI  (build web + go vet + go test + go build)
        │
        │  autoDeploy
        ▼
Easypanel  ──docker build──►  image  ──swarm update──►  app_9router-go
```

`Release` hanya jalan saat ada tag `v*` (rilis upstream), tidak tiap sync.
