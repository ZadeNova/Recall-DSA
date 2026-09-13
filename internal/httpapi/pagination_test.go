package httpapi

import (
	"net/url"
	"reflect"
	"testing"
)

// TestHiddenFieldsFrom_SortedAndFlattened guards against a real gap: the
// #7 pagination refactor (deriving the page-size form's hidden fields
// from the same url.Values already used to build Prev/Next URLs) had no
// direct test at all — it was only checked manually with curl. url.Values
// is a map, so iteration order isn't stable; hiddenFieldsFrom must sort
// by key for deterministic output, and must emit one hiddenField per
// value for a key with more than one (a case a naive "one input per key"
// implementation would drop).
func TestHiddenFieldsFrom_SortedAndFlattened(t *testing.T) {
	extra := url.Values{
		"topic":      {"Arrays"},
		"difficulty": {"Easy"},
		"tag":        {"a", "b"}, // multi-value: both must appear
	}

	got := hiddenFieldsFrom(extra)
	want := []hiddenField{
		{Name: "difficulty", Value: "Easy"},
		{Name: "tag", Value: "a"},
		{Name: "tag", Value: "b"},
		{Name: "topic", Value: "Arrays"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hiddenFieldsFrom(%v) = %+v, want %+v", extra, got, want)
	}

	if got := hiddenFieldsFrom(url.Values{}); len(got) != 0 {
		t.Errorf("hiddenFieldsFrom(empty) = %+v, want empty", got)
	}
}

// TestBuildPageInfo_PageSizeOptionsMatchValidPageSizes guards against #7:
// the page-size dropdown's options must come from the same source the
// server actually accepts (validPageSizes), not a second hardcoded list
// that could silently drift from it.
func TestBuildPageInfo_PageSizeOptionsMatchValidPageSizes(t *testing.T) {
	info := buildPageInfo("/library", url.Values{}, "page", "page_size", 1, 10, 0)
	if len(info.PageSizeOptions) != len(validPageSizes) {
		t.Fatalf("len(PageSizeOptions) = %d, want %d (one per validPageSizes entry)", len(info.PageSizeOptions), len(validPageSizes))
	}
	for _, n := range info.PageSizeOptions {
		if !validPageSizes[n] {
			t.Errorf("PageSizeOptions contains %d, not in validPageSizes", n)
		}
	}
	for n := range validPageSizes {
		found := false
		for _, opt := range info.PageSizeOptions {
			if opt == n {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("validPageSizes has %d, missing from PageSizeOptions", n)
		}
	}
}
