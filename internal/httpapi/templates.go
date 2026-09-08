package httpapi

import (
	"embed"
	"html/template"
)

//go:embed templates/*.html templates/partials/*.html
var templateFS embed.FS

// pageTemplates holds one *template.Template per page. Each is parsed
// from layout.html + the shared partials + exactly one page file:
// html/template's {{define}} blocks share a single namespace within one
// *template.Template, so parsing all pages together would make every
// page's {{define "content"}} silently overwrite the last one parsed.
// Parsing them separately avoids that collision entirely; the partials
// (which each define a uniquely-named block, e.g. "due-table") are safe
// to include in every page's set even when unused.
type pageTemplates struct {
	home       *template.Template
	due        *template.Template
	addForm    *template.Template
	editForm   *template.Template
	library    *template.Template
	topics     *template.Template
	importPage *template.Template
	errorPage  *template.Template
}

func loadTemplates() (*pageTemplates, error) {
	parse := func(page string) (*template.Template, error) {
		return template.ParseFS(templateFS,
			"templates/layout.html",
			"templates/partials/*.html",
			"templates/"+page,
		)
	}

	var t pageTemplates
	var err error
	if t.home, err = parse("home.html"); err != nil {
		return nil, err
	}
	if t.due, err = parse("due.html"); err != nil {
		return nil, err
	}
	if t.addForm, err = parse("add.html"); err != nil {
		return nil, err
	}
	if t.editForm, err = parse("edit.html"); err != nil {
		return nil, err
	}
	if t.library, err = parse("library.html"); err != nil {
		return nil, err
	}
	if t.topics, err = parse("topics.html"); err != nil {
		return nil, err
	}
	if t.importPage, err = parse("import.html"); err != nil {
		return nil, err
	}
	if t.errorPage, err = parse("error.html"); err != nil {
		return nil, err
	}
	return &t, nil
}
