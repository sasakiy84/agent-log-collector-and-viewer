package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/importer"
)

type watchFolder struct {
	Dir     string   `json:"dir"`
	Source  string   `json:"source,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
	Project string   `json:"project,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}
type config struct {
	Watch         []watchFolder `json:"watch"`
	WatchInterval string        `json:"watch_interval,omitempty"`
}

func loadWatchConfig(file string) ([]importer.Options, time.Duration, error) {
	interval := time.Second
	if file == "" {
		return nil, interval, nil
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	var c config
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return nil, 0, fmt.Errorf("config: %w", err)
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return nil, 0, fmt.Errorf("config: expected one JSON object")
	}
	if c.WatchInterval != "" {
		interval, err = time.ParseDuration(c.WatchInterval)
		if err != nil || interval <= 0 {
			return nil, 0, fmt.Errorf("watch_interval must be a positive duration")
		}
	}
	resolve := func(p string) (string, error) {
		if p == "~" || strings.HasPrefix(p, "~/") {
			home, e := os.UserHomeDir()
			if e != nil {
				return "", e
			}
			if p == "~" {
				p = home
			} else {
				p = filepath.Join(home, strings.TrimPrefix(p, "~/"))
			}
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(file), p)
		}
		return filepath.Abs(p)
	}
	out := make([]importer.Options, 0, len(c.Watch))
	for i, w := range c.Watch {
		if strings.TrimSpace(w.Dir) == "" {
			return nil, 0, fmt.Errorf("watch[%d]: dir is required", i)
		}
		if w.Source != "" && w.Source != "auto" && w.Source != "codex" && w.Source != "claude" && w.Source != "copilot" {
			return nil, 0, fmt.Errorf("watch[%d]: invalid source %q", i, w.Source)
		}
		dir, e := resolve(w.Dir)
		if e != nil {
			return nil, 0, e
		}
		cwd := ""
		if w.Cwd != "" {
			cwd, e = resolve(w.Cwd)
			if e != nil {
				return nil, 0, e
			}
		}
		out = append(out, importer.Options{Dirs: []string{dir}, Source: w.Source, WorkingDir: cwd, Project: w.Project, Tags: w.Tags})
	}
	return out, interval, nil
}
