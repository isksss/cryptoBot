package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWrapManagementAllowsHealthWithoutAuthentication(t *testing.T) {
	t.Parallel()

	protected := NewBasicProtector("cryptobot", "admin", "secret").WrapManagement(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestWrapManagementRejectsProtectedRouteWithoutAuthentication(t *testing.T) {
	t.Parallel()

	protected := NewBasicProtector("cryptobot", "admin", "secret").WrapManagement(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/summary", nil)
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatal("expected WWW-Authenticate header")
	}
}

func TestWrapManagementAllowsAuthenticatedUIRequest(t *testing.T) {
	t.Parallel()

	protected := NewBasicProtector("cryptobot", "admin", "secret").WrapManagement(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/ui/actions/orders", nil)
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestWrapManagementRejectsWrongCredentialsOnRoot(t *testing.T) {
	t.Parallel()

	protected := NewBasicProtector("cryptobot", "admin", "secret").WrapManagement(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "wrong")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}
