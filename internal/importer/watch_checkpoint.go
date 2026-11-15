package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

// Bump when transcript normalization changes, so unchanged source files are
// reconsidered once with the new parser. This is not an on-disk DB version.
const watchParserVersion = "watch-v1"

func watchKey(o Options, file string) string {
	tags := append([]string{}, o.Tags...)
	sort.Strings(tags)
	token := sha256.Sum256([]byte(o.Token))
	data, _ := json.Marshal([]any{watchParserVersion, file, o.Source, o.WorkingDir, o.Project, tags, o.Endpoint, hex.EncodeToString(token[:])})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fileIdentity(info os.FileInfo) string {
	// Use stable identity/change fields across Unix and Windows. Access time is
	// deliberately excluded: reading a transcript must not invalidate its checkpoint.
	value := reflect.ValueOf(info.Sys())
	if !value.IsValid() {
		return ""
	}
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return ""
	}
	identity := ""
	for _, name := range []string{"Dev", "Ino", "Ctim", "Ctimespec", "CreationTime"} {
		field := value.FieldByName(name)
		if field.IsValid() && field.CanInterface() {
			identity += fmt.Sprintf("%s=%v;", name, field.Interface())
		}
	}
	return identity
}

type fileStamp struct {
	Path           string
	Size, Modified int64
	Identity       string
}

func stamp(path string, info os.FileInfo) fileStamp {
	return fileStamp{path, info.Size(), info.ModTime().UnixNano(), fileIdentity(info)}
}

func watchFingerprint(ctx context.Context, file string) ([]byte, error) {
	info, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", file)
	}
	stamps := []fileStamp{stamp(file, info)}
	// A Copilot head depends on rotated predecessor journals, even if the head's
	// own bytes have not changed. Include their metadata without reading bodies.
	if filepath.Base(file) == "events.jsonl" {
		entries, err := os.ReadDir(filepath.Dir(file))
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !journalSegment(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			metadata, err := entry.Info()
			if err != nil {
				return nil, err
			}
			if metadata.Mode().IsRegular() {
				stamps = append(stamps, stamp(entry.Name(), metadata))
			}
		}
	}
	data, err := json.Marshal(stamps)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	return sum[:], nil
}
