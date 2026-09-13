package httpapi

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

const themeCookieName = "theme"
const defaultTheme = "light"

var validThemes = map[string]bool{"light": true, "dark": true, "nord": true}

// layoutData wraps every page's own view data with the two things
// layout.html needs that no content template does: the active theme and
// which nav item (if any) corresponds to the current page. Content
// templates are unaffected — they still receive exactly their own data
// via {{template "content" .Page}} in layout.html.
type layoutData struct {
	Theme    string
	Active   string
	DueCount int
	Page     any
}

// render executes tpl's "layout" definition with the visitor's current
// theme, active-nav-item, and due count attached, so every page (and
// the shared error/form-error paths) gets all three without each page's
// own data type needing fields for them.
func (s *Server) render(w http.ResponseWriter, r *http.Request, tpl *template.Template, data any) {
	wrapped := layoutData{Theme: themeFromRequest(r), Active: navActiveFor(r.URL.Path), DueCount: s.dueCount(r), Page: data}
	if err := tpl.ExecuteTemplate(w, "layout", wrapped); err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err)
	}
}

// dueCount is the nav's "N due today" pill value. It's decorative, not
// the actionable Due list itself (RecommendDue remains the sole source
// of truth for that), so a query failure fails open to 0 rather than
// breaking every page's render over a nav badge.
func (s *Server) dueCount(r *http.Request) int {
	n, err := s.svc.CountDue(r.Context())
	if err != nil {
		return 0
	}
	return n
}

// navActiveFor maps a request path to the nav item it corresponds to, if
// any — e.g. the edit-problem page isn't reachable from the nav at all,
// so it deliberately highlights nothing rather than guessing.
func navActiveFor(path string) string {
	switch {
	case path == "/":
		return "home"
	case strings.HasPrefix(path, "/due"):
		return "due"
	case strings.HasPrefix(path, "/library"):
		return "library"
	case path == "/problems/new" || path == "/problems":
		return "add"
	case strings.HasPrefix(path, "/problems/import"):
		return "import"
	case strings.HasPrefix(path, "/topics"):
		return "topics"
	default:
		return ""
	}
}

// themeFromRequest reads the visitor's theme preference from their
// cookie, defaulting to "light" if it's absent or not one of the three
// known themes (FRONTEND.md: Rose Pine Dawn/Moon, Nord).
func themeFromRequest(r *http.Request) string {
	c, err := r.Cookie(themeCookieName)
	if err != nil || !validThemes[c.Value] {
		return defaultTheme
	}
	return c.Value
}

// handleSetTheme persists the visitor's theme choice as a long-lived
// cookie and redirects back to wherever they came from. No client-side
// JS is involved — the server picks the theme before rendering, so
// there's no flash-of-wrong-theme on load.
func (s *Server) handleSetTheme(w http.ResponseWriter, r *http.Request) {
	theme := r.PathValue("name")
	if !validThemes[theme] {
		s.renderError(w, r, http.StatusBadRequest, fmt.Errorf("unknown theme %q", theme))
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:   themeCookieName,
		Value:  theme,
		Path:   "/",
		MaxAge: 365 * 24 * 60 * 60,
	})

	// The live theme-switcher script (layout.html) sets this header when
	// it's already toggled data-theme on the page directly — a redirect
	// would tear down and rebuild the page it just live-updated, which
	// is exactly the reload-driven "shake" this exists to avoid. A
	// plain <a href> click (JS disabled) never sends it, so that path
	// keeps working via the redirect below.
	if r.Header.Get("X-Theme-Live") == "true" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Redirect(w, r, localRedirectTarget(r.Referer(), r.Host), http.StatusSeeOther)
}

// localRedirectTarget reduces a Referer to a path on this site, falling
// back to "/" for anything else. Referer is chosen by the client, so
// forwarding it into a redirect unchecked lets any page that links here
// decide where the visitor lands — including an external origin. Only a
// path on this same host survives, and only the path and query of it,
// never the scheme or host: a foreign host is dropped, and so is a
// leading "//" or "/\", which browsers read as a protocol-relative URL
// to somewhere else even though it looks like a local path.
func localRedirectTarget(referer, host string) string {
	const fallback = "/"
	if referer == "" {
		return fallback
	}
	parsed, err := url.Parse(referer)
	if err != nil {
		return fallback
	}
	// A same-origin Referer is an absolute URL, so its own host is
	// expected — it just has to be this one.
	if parsed.Host != "" && parsed.Host != host {
		return fallback
	}
	// Validate the decoded path but emit the escaped one: "/%5Cevil" and
	// "/\evil" are the same target, and only the decoded form makes that
	// visible.
	if p := parsed.Path; !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return fallback
	}
	if parsed.RawQuery != "" {
		return parsed.EscapedPath() + "?" + parsed.RawQuery
	}
	return parsed.EscapedPath()
}
