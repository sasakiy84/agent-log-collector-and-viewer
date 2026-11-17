package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const apiKeyPrefix = "alk_"

type APIKey struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Key       string `json:"key"`
	CreatedAt string `json:"created_at"`
}

func bearerCredential(r *http.Request) (string, bool) {
	scheme, value, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	return value, ok && strings.EqualFold(scheme, "Bearer")
}
func (a *App) ownerBearer(r *http.Request) bool {
	value, ok := bearerCredential(r)
	return ok && a.token != "" && subtle.ConstantTimeCompare([]byte(value), []byte(a.token)) == 1
}
func apiKeyCredential(r *http.Request) (string, bool) {
	value, ok := bearerCredential(r)
	return value, ok && strings.HasPrefix(value, apiKeyPrefix)
}

// Explicit API-key credentials never fall back to the browser's owner cookie.
// Lookup on each request makes deletion effective for the very next request.
func (a *App) authorizeReadKey(w http.ResponseWriter, r *http.Request, key string) bool {
	if !a.serviceAvailable(w, r) {
		return false
	}
	digest := sha256.Sum256([]byte(key))
	var valid bool
	if err := a.readers.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM api_keys WHERE key_hash=?)", digest[:]).Scan(&valid); err != nil {
		databaseError(w, r, err)
		return false
	}
	if !valid {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || strings.HasPrefix(r.URL.Path, "/api/settings") {
		http.Error(w, "API keys grant read-only data access", http.StatusForbidden)
		return false
	}
	return true
}

func (a *App) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := a.readers.QueryContext(r.Context(), "SELECT id,name,key,created_at FROM api_keys ORDER BY created_at DESC,id")
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer rows.Close()
	keys := []APIKey{}
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Key, &k.CreatedAt); err != nil {
			databaseError(w, r, err)
			return
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, map[string]any{"keys": keys})
}
func apiKeyName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Name string `json:"name"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return "", false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "Invalid JSON", 400)
		return "", false
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || utf8.RuneCountInString(body.Name) > 120 {
		http.Error(w, "Name must contain 1–120 characters", 400)
		return "", false
	}
	return body.Name, true
}
func (a *App) createAPIKey(w http.ResponseWriter, r *http.Request) {
	name, ok := apiKeyName(w, r)
	if !ok {
		return
	}
	var random [48]byte
	if _, err := rand.Read(random[:]); err != nil {
		http.Error(w, "Unable to generate key", 500)
		return
	}
	key := APIKey{ID: hex.EncodeToString(random[:16]), Name: name, Key: apiKeyPrefix + base64.RawURLEncoding.EncodeToString(random[16:]), CreatedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")}
	hash := sha256.Sum256([]byte(key.Key))
	if _, err := a.DB.ExecContext(r.Context(), "INSERT INTO api_keys VALUES(?,?,?,?,?)", key.ID, key.Name, key.Key, hash[:], key.CreatedAt); err != nil {
		databaseError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	write(w, key)
}
func (a *App) renameAPIKey(w http.ResponseWriter, r *http.Request) {
	name, ok := apiKeyName(w, r)
	if !ok {
		return
	}
	result, err := a.DB.ExecContext(r.Context(), "UPDATE api_keys SET name=? WHERE id=?", name, r.PathValue("id"))
	if !apiKeyChanged(w, r, result, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *App) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	result, err := a.DB.ExecContext(r.Context(), "DELETE FROM api_keys WHERE id=?", r.PathValue("id"))
	if !apiKeyChanged(w, r, result, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func apiKeyChanged(w http.ResponseWriter, r *http.Request, result sql.Result, err error) bool {
	if err == nil {
		var n int64
		n, err = result.RowsAffected()
		if err == nil && n == 0 {
			err = sql.ErrNoRows
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return false
	}
	if err != nil {
		databaseError(w, r, err)
		return false
	}
	return true
}
