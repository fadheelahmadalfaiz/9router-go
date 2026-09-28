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
        ├─►  CI  (web build + go vet + go test + go build)
        │
        └─►  Publish Image  (multi-arch → GHCR, cache GHA)
                    │
                    │  ghcr.io/fadheelahmadalfaiz/9router-go:tag
                    ▼
              Easypanel  ──docker pull──►  app_9router-go
```

`Release` hanya jalan saat ada tag `v*` (rilis upstream), tidak tiap sync.

---

## Image: `ghcr.io/fadheelahmadalfaiz/9router-go`

Workflow `.github/workflows/publish-image.yml` mem-build image multi-arch
(`linux/amd64` + `linux/arm64`) lalu push ke GHCR setiap ada push ke `main`.

### Tags

| Tag         | Arti                                                       |
| ----------- | ---------------------------------------------------------- |
| `latest`    | Build terbaru dari `main`                                   |
| `main`      | Alias bergerak, sama dengan `latest`                         |
| `sha-xxxxxx`| Commit spesifik, immutable — dipakai untuk rollback        |
| `<VERSION>` | Dari file `VERSION`, misal `1.9.5`                          |
| `v<VERSION>`| Sama dengan di atas, mengikuti konvensi tag rilis            |

### Pakai di server

```bash
docker pull ghcr.io/fadheelahmadalfaiz/9router-go:latest

docker run -d --name 9router-go --restart unless-stopped \
  -p 20130:20130 \
  -v /etc/easypanel/projects/app/9router-go/data:/root/.9router \
  -e PORT=20130 \
  -e INITIAL_PASSWORD='your-password' \
  ghcr.io/fadheelahmadalfaiz/9router-go:latest
```

### Trigger manual (dry run)

Lewat tab **Actions → Publish Image → Run workflow**, uncheck `push` untuk
build tanpa push — berguna saat mau memastikan build-nya sehat dulu.

### Build lambat, cache cepat

Multi-arch lewat QEMU took **~13 menit** untuk build pertama. Setelah itu
`cache-from`/`cache-to: type=gha,mode=max` membuat build berikutnya jauh lebih
cepat. Job memakai `cancel-in-progress: true`, jadi kalau upstream commit
menyusul saat build berjalan, yang lama dibatalkan dan yang baru pakai cache.
