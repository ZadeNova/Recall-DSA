package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestThemeFromRequest(t *testing.T) {
	cases := []struct {
		name      string
		cookie    string // "" means no cookie at all
		hasCookie bool
		want      string
	}{
		{"no_cookie_defaults_to_light", "", false, "light"},
		{"valid_light", "light", true, "light"},
		{"valid_dark", "dark", true, "dark"},
		{"valid_nord", "nord", true, "nord"},
		{"invalid_falls_back_to_light", "solarized", true, "light"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.hasCookie {
				r.AddCookie(&http.Cookie{Name: themeCookieName, Value: c.cookie})
			}
			if got := themeFromRequest(r); got != c.want {
				t.Errorf("themeFromRequest(cookie=%q, present=%v) = %q, want %q", c.cookie, c.hasCookie, got, c.want)
			}
		})
	}
}

func TestHandleSetTheme_RedirectsToRefererOrFallsBackHome(t *testing.T) {
	cases := []struct {
		name    string
		referer string // "" means no Referer header at all
		wantLoc string
	}{
		{"redirects_to_referer", "/library", "/library"},
		{"falls_back_to_home_with_no_referer", "", "/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _ := newTestServer(t)

			req := httptest.NewRequest(http.MethodGet, "/theme/dark", nil)
			if c.referer != "" {
				req.Header.Set("Referer", c.referer)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", rec.Code)
			}
			if got := rec.Header().Get("Location"); got != c.wantLoc {
				t.Errorf("Location = %q, want %q", got, c.wantLoc)
			}

			cookies := rec.Result().Cookies()
			if len(cookies) != 1 || cookies[0].Name != themeCookieName || cookies[0].Value != "dark" {
				t.Errorf("cookies = %+v, want one theme=dark cookie", cookies)
			}
		})
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

// TestLocalRedirectTarget_RejectsOffSiteReferers covers the theme
// switcher's post-set redirect. It sends the visitor back to
// r.Referer(), which the client chooses — so forwarding it unchecked let
// any page linking to /theme/{name} pick where the visitor lands,
// including an external origin. Only a path on this same host survives.
func TestLocalRedirectTarget_RejectsOffSiteReferers(t *testing.T) {
	const host = "recall.tailnet:8080"
	cases := []struct {
		name    string
		referer string
		want    string
	}{
		{"same_host_absolute", "http://recall.tailnet:8080/due", "/due"},
		{"same_host_keeps_query", "http://recall.tailnet:8080/library?topic=Graphs&page=2", "/library?topic=Graphs&page=2"},
		{"relative_path", "/topics", "/topics"},
		{"empty", "", "/"},
		{"foreign_host", "https://evil.example/phish", "/"},
		{"foreign_host_lookalike_path", "https://evil.example/due", "/"},
		// Browsers read a leading "//" as protocol-relative — "//evil.example"
		// is another origin, not a local path, despite starting with a slash.
		{"protocol_relative", "//evil.example/phish", "/"},
		{"backslash_smuggle", "/\\evil.example", "/"},
		{"scheme_only", "javascript:alert(1)", "/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := localRedirectTarget(c.referer, host); got != c.want {
				t.Errorf("localRedirectTarget(%q, %q) = %q, want %q", c.referer, host, got, c.want)
			}
		})
	}
}

// TestHandleSetTheme_IgnoresOffSiteReferer is the wiring check for
// localRedirectTarget: the unit test above proves the function is
// correct, this proves the handler actually routes the Referer through
// it rather than forwarding it raw.
func TestHandleSetTheme_IgnoresOffSiteReferer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/theme/dark", nil)
	req.Header.Set("Referer", "https://evil.example/phish")
	rec := httptest.NewRecorder()
	h, _ := newTestServer(t)
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Location"); got != "/" {
		t.Errorf("Location = %q, want %q — an off-site Referer must not choose where the visitor lands", got, "/")
	}
}
