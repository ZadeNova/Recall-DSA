package httpapi

import "embed"

// assetsFS holds self-hosted static assets: the two IBM Plex font files
// (FRONTEND.md, Typography) and htmx itself (FRONTEND.md, Decided UX
// facts — htmx-based grading). All fetched once from their upstream
// source and committed here, then served by this binary — consistent
// with the project's no-CDN-dependency stance: a self-hoster's Pi never
// needs outbound internet just to render the page correctly.
//
//go:embed assets/fonts/*.woff2 assets/htmx.min.js
var assetsFS embed.FS
