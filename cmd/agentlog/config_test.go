package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agentlog.json")
	if err := os.WriteFile(p, []byte(`{"watch_interval":"250ms","watch":[{"dir":"sessions","source":"claude","cwd":"work/shop","project":"shop","tags":["backend","go"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	options, interval, err := loadWatchConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if interval != 250*time.Millisecond || len(options) != 1 || options[0].Dirs[0] != filepath.Join(dir, "sessions") || options[0].WorkingDir != filepath.Join(dir, "work/shop") || options[0].Project != "shop" || len(options[0].Tags) != 2 {
		t.Fatal(options, interval)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"watch":[{"dir":"~/.codex/sessions"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	options, interval, err = loadWatchConfig(p)
	if err != nil || interval != time.Second || options[0].Dirs[0] != filepath.Join(home, ".codex/sessions") {
		t.Fatal(options, interval, err)
	}
}
func TestInvalidWatchConfig(t *testing.T) {
	for _, body := range []string{`{"watch":[{}]}`, `{"watch":[{"dir":"x","source":"unknown"}]}`, `{"watch":[{"dir":"x","tag":["typo"]}]}`, `{"watch":[],"watch_interval":"0s"}`, `{"watch_interval":"wrong"}`, `{"watch":[]} {}`, `{`} {
		p := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := loadWatchConfig(p); err == nil {
			t.Fatalf("invalid config accepted: %s", body)
		}
	}
}
