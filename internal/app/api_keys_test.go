package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func keyRequest(a *App, method, path, body, bearer string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func newTestAPIKey(t *testing.T, a *App) APIKey {
	t.Helper()
	w := keyRequest(a, "POST", "/api/settings/api-keys", `{"name":"Research agent"}`, token)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var key APIKey
	if err := json.Unmarshal(w.Body.Bytes(), &key); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key.Key, apiKeyPrefix) || len(key.Key) < 40 || key.ID == "" {
		t.Fatal("invalid generated key")
	}
	return key
}
func TestAPIKeysGrantDataReadsWithoutOwnerAccess(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "question", "otel", "codex", "root", "gen_ai.user.message", map[string]any{"prompt": "hello"}))
	key := newTestAPIKey(t, a)
	for _, path := range []string{"/api/version", "/api/readme", "/api/metrics", "/api/status", "/api/projects", "/api/sessions", "/api/session?id=codex:root", "/api/skills", "/api/export?session=codex:root"} {
		w := keyRequest(a, "GET", path, "", key.Key)
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), key.Key) {
			t.Fatal("read endpoint exposed API key", path)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("credentialed response may be cached", path)
		}
	}
	lower := httptest.NewRequest("GET", "/api/metrics", nil)
	lower.Header.Set("Authorization", "bearer "+key.Key)
	lowerResponse := httptest.NewRecorder()
	a.Handler().ServeHTTP(lowerResponse, lower)
	if lowerResponse.Code != 200 {
		t.Fatal("Bearer scheme must be case insensitive", lowerResponse.Code)
	}
	for _, test := range []struct{ method, path, body string }{
		{"GET", "/api/settings/api-keys", ""}, {"HEAD", "/api/settings/api-keys", ""},
		{"POST", "/api/settings/api-keys", `{"name":"another"}`},
		{"PATCH", "/api/settings/api-keys/" + key.ID, `{"name":"changed"}`},
		{"DELETE", "/api/settings/api-keys/" + key.ID, ""},
		{"POST", "/api/archives", `{}`},
	} {
		w := keyRequest(a, test.method, test.path, test.body, key.Key)
		if w.Code != 403 {
			t.Fatal(test.method, test.path, w.Code)
		}
	}
	if w := keyRequest(a, "POST", "/v1/logs", `{}`, key.Key); w.Code != 401 {
		t.Fatal("read key ingested telemetry", w.Code)
	}
	if w := keyRequest(a, "POST", "/api/login", `{"Token":"`+key.Key+`"}`, ""); w.Code != 401 || len(w.Result().Cookies()) != 0 {
		t.Fatal("key created owner login", w.Code)
	}
	for _, credential := range []string{"", key.Key + "x"} {
		if w := keyRequest(a, "GET", "/api/metrics", "", credential); w.Code != 401 {
			t.Fatal("invalid key accepted", w.Code)
		}
	}
	// Owner sessions can manage keys; API-key credentials cannot borrow that role.
	login := keyRequest(a, "POST", "/api/login", `{"Token":"`+token+`"}`, "")
	r := httptest.NewRequest("GET", "/api/settings/api-keys", nil)
	r.AddCookie(login.Result().Cookies()[0])
	r.Header.Set("Authorization", "Bearer "+key.Key)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("key borrowed cookie privilege", w.Code)
	}
	if w := keyRequest(a, "PATCH", "/api/settings/api-keys/"+key.ID, `{"name":"日本語のエージェント"}`, token); w.Code != 204 {
		t.Fatal(w.Code)
	}
	w = keyRequest(a, "GET", "/api/settings/api-keys", "", token)
	var list struct{ Keys []APIKey }
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Keys) != 1 || list.Keys[0].Name != "日本語のエージェント" || list.Keys[0].Key != key.Key {
		t.Fatal("rename changed key")
	}
	if w := keyRequest(a, "DELETE", "/api/settings/api-keys/"+key.ID, "", token); w.Code != 204 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("GET", "/api/metrics", nil)
	r.AddCookie(login.Result().Cookies()[0])
	r.Header.Set("Authorization", "Bearer "+key.Key)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("deleted key remained valid", w.Code)
	}
	if w := keyRequest(a, "DELETE", "/api/settings/api-keys/"+key.ID, "", token); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
func TestAPIKeysAreRecoverableAfterRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "keys.db")
	a, err := Open(context.Background(), file, token)
	if err != nil {
		t.Fatal(err)
	}
	key := newTestAPIKey(t, a)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = Open(context.Background(), file, token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	w := keyRequest(a, "GET", "/api/settings/api-keys", "", token)
	var list struct{ Keys []APIKey }
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Keys) != 1 || list.Keys[0] != key {
		t.Fatal("saved key was not recoverable")
	}
	if w := keyRequest(a, "GET", "/api/readme", "", key.Key); w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestAPIKeyManagementOwnerCookieAndCrossOriginProtection(t *testing.T) {
	a := setup(t)
	login := keyRequest(a, "POST", "/api/login", `{"Token":"`+token+`"}`, "")
	for _, origin := range []string{"https://untrusted.example", ""} {
		r := httptest.NewRequest("POST", "/api/settings/api-keys", strings.NewReader(`{"name":"browser key"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(login.Result().Cookies()[0])
		if origin != "" {
			r.Header.Set("Origin", origin)
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		want := 201
		if origin != "" {
			want = 403
		}
		if w.Code != want {
			t.Fatal(origin, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{}`, `{"name":" "}`, `{"name":"` + strings.Repeat("あ", 121) + `"}`, `not JSON`, `{"name":"valid"} trailing`, `{"name":"valid","key":"ignored"}`} {
		if w := keyRequest(a, "POST", "/api/settings/api-keys", body, token); w.Code != 400 {
			t.Fatal(body, w.Code)
		}
	}
}
func TestAPIKeysKeepReadScopeWhenOwnerAuthenticationIsDisabled(t *testing.T) {
	a := setup(t)
	key := newTestAPIKey(t, a)
	a.token = ""
	for _, path := range []string{"/api/version", "/api/readme", "/api/metrics", "/api/settings/api-keys"} {
		if w := keyRequest(a, "GET", path, "", ""); w.Code != 200 {
			t.Fatal("local auth behavior changed", path, w.Code)
		}
	}
	if w := keyRequest(a, "GET", "/api/readme", "", key.Key); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := keyRequest(a, "GET", "/api/settings/api-keys", "", key.Key); w.Code != 403 {
		t.Fatal("explicit read key gained management access", w.Code)
	}
	if w := keyRequest(a, "GET", "/api/readme", "", key.Key+"x"); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := keyRequest(a, "POST", "/v1/logs", `{}`, key.Key); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
