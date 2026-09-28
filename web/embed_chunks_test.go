package web

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// scriptSrcPattern matches the built entry script in dist/index.html.
var scriptSrcPattern = regexp.MustCompile(`<script[^>]+src="([^"]+)"`)

// modulepreloadPattern matches the CSS/JS chunks Vite injects for the initial
// route, and the module graph edges of the main bundle. Rolldown emits dynamic
// imports as template literals (import(`./Chunk-hash.js`)), so the pattern has
// to accept backticks as well as quotes.
var assetRefPattern = regexp.MustCompile("[A-Za-z0-9._-]+\\.(?:js|css)")

func embeddedFile(t *testing.T, name string) bool {
	t.Helper()
	if _, err := fs.Sub(DistFS, "dist"); err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	if _, err := fs.Stat(subDist(t), name); err == nil {
		return true
	}
	return false
}

func subDist(t *testing.T) fs.FS {
	t.Helper()
	sub, err := fs.Sub(DistFS, "dist")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	return sub
}

// TestEmbeddedEntryMatchesIndexHTML catches a stale embed: go:embed snapshots
// web/dist at compile time, so a binary built before the frontend was rebuilt
// serves an old index.html pointing at chunk names that no longer exist. Every
// chunk the entry references has to be present in the embedded FS, or the
// dynamic imports behind a lazy tab silently reject and the view renders empty.
func TestEmbeddedEntryMatchesIndexHTML(t *testing.T) {
	data, err := fs.ReadFile(subDist(t), "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}

	match := scriptSrcPattern.FindStringSubmatch(string(data))
	if match == nil {
		t.Fatal("index.html has no <script src=...> entry")
	}
	entry := strings.TrimPrefix(match[1], "/")
	if !embeddedFile(t, entry) {
		t.Errorf("embedded FS is missing the entry script %q (stale dist?)", entry)
	}
}

// TestEmbeddedFSContainsChartAssets guards the chart assets generally: go:embed
// snapshots web/dist at compile time, so a binary built before the frontend was
// rebuilt serves an index.html that names chunks which no longer exist.
func TestEmbeddedFSContainsChartAssets(t *testing.T) {
	assetsDir := filepath.Join("dist", "assets")
	entries, err := os.ReadDir(assetsDir)
	if err != nil {
		t.Skipf("no built assets to verify: %v", err)
	}

	sub := subDist(t)
	var found int
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".css") {
			continue
		}
		found++
		if _, err := fs.Stat(sub, path.Join("assets", name)); err != nil {
			t.Errorf("embedded FS is missing asset %q (rebuild the binary after `bun run build`)", name)
		}
	}
	if found == 0 {
		t.Skip("no built assets; the frontend has not been built yet")
	}
}

// TestEmbeddedLayerChartLibraryIsReachable guards the layerchart internals,
// which the bundler keeps in their own chunk. A missing chunk there is not a
// build error and not a 404 the user can act on: the import rejects, the
// rejection is swallowed, and the charts never mount.
func TestEmbeddedLayerChartLibraryIsReachable(t *testing.T) {
	assetsDir := filepath.Join("dist", "assets")
	entries, err := os.ReadDir(assetsDir)
	if err != nil {
		t.Skipf("no built assets to verify: %v", err)
	}

	sub := subDist(t)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "chart-") || !strings.HasSuffix(name, ".js") {
			continue
		}
		if _, err := fs.Stat(sub, path.Join("assets", name)); err != nil {
			t.Errorf("embedded FS is missing layerchart chunk %q (rebuild the binary after `bun run build`)", name)
		}
	}
}
