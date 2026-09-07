package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestThemeFromRequest_DefaultsToLight(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := themeFromRequest(r); got != "light" {
		t.Errorf("themeFromRequest(no cookie) = %q, want %q", got, "light")
	}
}

func TestThemeFromRequest_ValidCookie(t *testing.T) {
	for _, theme := range []string{"light", "dark", "nord"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{Name: themeCookieName, Value: theme})
		if got := themeFromRequest(r); got != theme {
			t.Errorf("themeFromRequest(cookie=%q) = %q, want %q", theme, got, theme)
		}
	}
}

func TestThemeFromRequest_InvalidCookieFallsBackToLight(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: themeCookieName, Value: "solarized"})
	if got := themeFromRequest(r); got != "light" {
		t.Errorf("themeFromRequest(invalid cookie) = %q, want %q", got, "light")
	}
}

func TestHandleSetTheme_SetsCookieAndRedirectsToReferer(t *testing.T) {
	h, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/theme/dark", nil)
	req.Header.Set("Referer", "/library")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/library" {
		t.Errorf("Location = %q, want %q", got, "/library")
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != themeCookieName || cookies[0].Value != "dark" {
		t.Errorf("cookies = %+v, want one theme=dark cookie", cookies)
	}
}

func TestHandleSetTheme_FallsBackToHomeWithNoReferer(t *testing.T) {
	h, _ := newTestServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/theme/nord", nil))

	if got := rec.Header().Get("Location"); got != "/" {
		t.Errorf("Location = %q, want %q (no Referer, fallback)", got, "/")
	}
}

func TestHandleSetTheme_UnknownThemeReturns400(t *testing.T) {
	h, _ := newTestServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/theme/solarized", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
