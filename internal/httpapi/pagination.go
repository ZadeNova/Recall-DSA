package httpapi

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
)

const defaultPageSize = 10

var validPageSizes = map[int]bool{10: true, 25: true, 50: true}

// hiddenField is one <input type="hidden"> the shared "pagination"
// partial renders inside its page-size form, so changing page size
// preserves whatever filters are active.
type hiddenField struct {
	Name  string
	Value string
}

// pageInfo is what every paginated list view feeds its template: current
// page, total, a human "Showing X-Y of N problems" label, Prev/Next URLs
// (empty when there's no such page), and everything the shared
// "pagination" partial needs to render its page-size form — shared by
// Due's due-now and upcoming tables, Home's glance list, and Library, so
// the page-clamping math, URL-building, and pagination markup all exist
// in exactly one place. HiddenFields is derived from the same `extra`
// values already used to build PrevURL/NextURL (see buildPageInfo) —
// deliberately the same source, not a second hand-typed list, so a
// page's filters can't preserve correctly in the URL but drop silently
// when the page-size dropdown is changed (or vice versa).
type pageInfo struct {
	Page          int
	PageSize      int
	TotalCount    int
	TotalPages    int
	ShowingText   string
	PrevURL       string
	NextURL       string
	PageSizeParam string
	HiddenFields  []hiddenField
}

// pageSizeFromQuery/pageFromQuery parse the page-size/page query params,
// falling back to sensible defaults — shared parsing so every paginated
// section validates the same way.
func pageSizeFromQuery(v string) int {
	if n, err := strconv.Atoi(v); err == nil && validPageSizes[n] {
		return n
	}
	return defaultPageSize
}

func pageFromQuery(v string) int {
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n
	}
	return 1
}

// totalPagesFor is always >= 1, even when total is 0, so "page 1 of 1"
// is well-defined for an empty result set.
func totalPagesFor(total, pageSize int) int {
	tp := (total + pageSize - 1) / pageSize
	if tp == 0 {
		return 1
	}
	return tp
}

// buildPageInfo assembles a pageInfo for a paginated list, given the
// FINAL page/pageSize/total (i.e. after the caller has already clamped
// an out-of-range page and re-queried if necessary — see handleLibrary
// for the reference pattern). extra carries whatever OTHER query params
// (search, filters, sort) should be preserved across Prev/Next links;
// pageParam/pageSizeParam let independent paginated sections on the same
// page (e.g. Due's due-now vs upcoming tables) use distinct query param
// names so they paginate independently without colliding.
func buildPageInfo(path string, extra url.Values, pageParam, pageSizeParam string, page, pageSize, total int) pageInfo {
	totalPages := totalPagesFor(total, pageSize)

	info := pageInfo{
		Page: page, PageSize: pageSize, TotalCount: total, TotalPages: totalPages,
		PageSizeParam: pageSizeParam,
		HiddenFields:  hiddenFieldsFrom(extra),
	}
	if total == 0 {
		info.ShowingText = "No problems match"
	} else {
		from := (page-1)*pageSize + 1
		to := from + pageSize - 1
		if to > total {
			to = total
		}
		info.ShowingText = fmt.Sprintf("Showing %d-%d of %d problems", from, to, total)
	}

	urlFor := func(p int) string {
		v := url.Values{}
		for k, vals := range extra {
			v[k] = vals
		}
		v.Set(pageParam, strconv.Itoa(p))
		v.Set(pageSizeParam, strconv.Itoa(pageSize))
		return path + "?" + v.Encode()
	}
	if page > 1 {
		info.PrevURL = urlFor(page - 1)
	}
	if page < totalPages {
		info.NextURL = urlFor(page + 1)
	}
	return info
}

// hiddenFieldsFrom flattens extra into a deterministically-ordered slice
// (sorted by key, since url.Values is a map and iteration order would
// otherwise vary run to run) for the shared "pagination" partial to
// render as hidden <input> fields.
func hiddenFieldsFrom(extra url.Values) []hiddenField {
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var fields []hiddenField
	for _, k := range keys {
		for _, v := range extra[k] {
			fields = append(fields, hiddenField{Name: k, Value: v})
		}
	}
	return fields
}
