package web

import (
	"errors"
	"net/url"
	"strconv"
)

var errBadPage = errors.New("invalid page")

// Page describes one page of a paginated list, following Django's Paginator
// rules: an empty list still has one page, ?page=last selects the final page,
// and any other non-integer or out-of-range page is an error (404).
type Page struct {
	Number   int
	NumPages int
	Count    int
	PerPage  int

	path  string
	query url.Values
}

func paginate(u *url.URL, count, perPage int) (*Page, error) {
	p := &Page{Count: count, PerPage: perPage, path: u.Path, query: u.Query()}
	p.NumPages = max(1, (count+perPage-1)/perPage)
	p.Number = 1
	if raw := p.query.Get("page"); raw != "" {
		if raw == "last" {
			p.Number = p.NumPages
		} else {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > p.NumPages {
				return nil, errBadPage
			}
			p.Number = n
		}
	}
	return p, nil
}

func (p *Page) Offset() int       { return (p.Number - 1) * p.PerPage }
func (p *Page) HasPrevious() bool { return p.Number > 1 }
func (p *Page) HasNext() bool     { return p.Number < p.NumPages }
func (p *Page) Previous() int     { return p.Number - 1 }
func (p *Page) Next() int         { return p.Number + 1 }

// URL returns the current path with the page parameter set to n, keeping any
// other query parameters.
func (p *Page) URL(n int) string {
	q := url.Values{}
	for k, v := range p.query {
		q[k] = v
	}
	q.Set("page", strconv.Itoa(n))
	return p.path + "?" + q.Encode()
}

// Window returns up to 7 page numbers centred on the current page, the same
// range as the Django page_window template filter.
func (p *Page) Window() []int {
	return pageWindow(p.Number, p.NumPages, 7)
}

func pageWindow(page, last, size int) []int {
	lo, hi := 1, min(size, last)
	if page >= size/2+1 {
		lo, hi = page-size/2, min(last, page+size/2)
	}
	var w []int
	for i := lo; i <= hi; i++ {
		w = append(w, i)
	}
	return w
}
