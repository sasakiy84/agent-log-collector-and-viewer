package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestEmptyTokenAllowsAnonymousReadWriteAndIngestion(t *testing.T) {
	a, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if w := request(a, "/v1/logs", logBody("codex", "local", "", ""), ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/v1/logs", "/v1/traces", "/v1/metrics"} {
		for _, auth := range []string{"", "old-token-is-ignored-in-open-mode"} {
			w := request(a, path, "{}", auth)
			if w.Code != 200 || w.Header().Get("X-Agentlog-Auth") != "disabled" {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
	}
	for _, path := range []string{"/api/status", "/api/sessions", "/api/skills", "/api/archive", "/api/session?id=codex:local", "/api/export?session=codex:local", "/api/export/download"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("X-Agentlog-Auth") != "disabled" {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	// No session cookie signed with an empty key is needed or issued.
	login := request(a, "/api/login", `{}`, "")
	if login.Code != 200 || len(login.Result().Cookies()) != 0 {
		t.Fatal(login.Code, login.Result().Cookies())
	}
}

func TestConfiguredTokenStillProtectsAllAccess(t *testing.T) {
	a := setup(t)
	for _, path := range []string{"/v1/logs", "/v1/traces", "/v1/metrics"} {
		for _, auth := range []string{"", "incorrect-token-value"} {
			w := request(a, path, "{}", auth)
			if w.Code != 401 || w.Header().Get("X-Agentlog-Auth") != "required" {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
		if w := request(a, path, "{}", token); w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/status", "/api/sessions", "/api/skills", "/api/export/download", "/api/archives"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal(path, w.Code)
		}
	}
	if w := request(a, "/api/login", `{"token":"wrong"}`, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
