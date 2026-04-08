package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// BasicProtector は管理 API と管理画面を Basic 認証で保護します。
type BasicProtector struct {
	realm    string
	username string
	password string
}

// NewBasicProtector は Basic 認証ミドルウェアを構築します。
func NewBasicProtector(realm string, username string, password string) *BasicProtector {
	if strings.TrimSpace(realm) == "" {
		realm = "restricted"
	}

	return &BasicProtector{
		realm:    realm,
		username: username,
		password: password,
	}
}

// WrapManagement は管理 API と管理画面系ルートだけを保護します。
func (p *BasicProtector) WrapManagement(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requiresAuthentication(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if p.isAuthorized(r) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("WWW-Authenticate", `Basic realm="`+p.realm+`", charset="UTF-8"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
	})
}

func requiresAuthentication(path string) bool {
	if path == "/healthz" {
		return false
	}
	return path == "/" || strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/ui/")
}

func (p *BasicProtector) isAuthorized(r *http.Request) bool {
	username, password, ok := r.BasicAuth()
	if !ok {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(username), []byte(p.username)) == 1 &&
		subtle.ConstantTimeCompare([]byte(password), []byte(p.password)) == 1
}
