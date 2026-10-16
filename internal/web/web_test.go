package web

import (
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/ggetzie/arai_go/internal/config"
)

func TestPageWindow(t *testing.T) {
	cases := []struct {
		page, last int
		want       []int
	}{
		{1, 1, []int{1}},
		{1, 3, []int{1, 2, 3}},
		{3, 20, []int{1, 2, 3, 4, 5, 6, 7}},
		{4, 20, []int{1, 2, 3, 4, 5, 6, 7}},
		{10, 20, []int{7, 8, 9, 10, 11, 12, 13}},
		{20, 20, []int{17, 18, 19, 20}},
	}
	for _, c := range cases {
		if got := pageWindow(c.page, c.last, 7); !reflect.DeepEqual(got, c.want) {
			t.Errorf("pageWindow(%d, %d) = %v, want %v", c.page, c.last, got, c.want)
		}
	}
}

func TestPaginate(t *testing.T) {
	cases := []struct {
		query     string
		count     int
		wantPage  int
		wantPages int
		wantErr   bool
	}{
		{"", 0, 1, 1, false},
		{"page=1", 0, 1, 1, false},
		{"page=2", 0, 0, 0, true},
		{"page=2", 26, 2, 2, false},
		{"page=last", 51, 3, 3, false},
		{"page=0", 51, 0, 0, true},
		{"page=abc", 51, 0, 0, true},
		{"page=4", 75, 0, 0, true},
	}
	for _, c := range cases {
		p, err := paginate(&url.URL{Path: "/", RawQuery: c.query}, c.count, 25)
		if (err != nil) != c.wantErr {
			t.Errorf("%q count %d: err %v", c.query, c.count, err)
			continue
		}
		if err == nil && (p.Number != c.wantPage || p.NumPages != c.wantPages) {
			t.Errorf("%q count %d: page %d of %d, want %d of %d", c.query, c.count, p.Number, p.NumPages, c.wantPage, c.wantPages)
		}
	}

	p, _ := paginate(&url.URL{Path: "/archive/", RawQuery: "q=a+b&page=2"}, 200, 50)
	if got := p.URL(3); got != "/archive/?page=3&q=a+b" {
		t.Errorf("URL(3) = %q", got)
	}
	if p.Offset() != 50 || !p.HasPrevious() || !p.HasNext() {
		t.Errorf("offset %d prev %v next %v", p.Offset(), p.HasPrevious(), p.HasNext())
	}
}

func TestParseDateParam(t *testing.T) {
	if d, ok := parseDateParam("20201001"); !ok || !d.Equal(time.Date(2020, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("20201001 -> %v %v", d, ok)
	}
	for _, bad := range []string{"2020101", "202010011", "2020-10-1", "20201301", "20200230", "abcdefgh"} {
		if _, ok := parseDateParam(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestIntcomma(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 84781: "84,781", 1234567: "1,234,567", -6939: "-6,939"} {
		if got := intcomma(in); got != want {
			t.Errorf("intcomma(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestAdminCookie(t *testing.T) {
	s := &Server{cfg: &config.Config{SecretKey: "0123456789abcdef0123456789abcdef", AdminUser: "gabe"}}
	now := time.Unix(1_800_000_000, 0)
	v := s.signAdmin("gabe", now.Add(time.Hour))

	if user, ok := s.verifyAdmin(v, now); !ok || user != "gabe" {
		t.Fatalf("valid cookie rejected")
	}
	if _, ok := s.verifyAdmin(v, now.Add(2*time.Hour)); ok {
		t.Error("expired cookie accepted")
	}
	if _, ok := s.verifyAdmin(v+"x", now); ok {
		t.Error("tampered signature accepted")
	}
	forged := s.signAdmin("mallory", now.Add(time.Hour))
	if _, ok := s.verifyAdmin(forged, now); ok {
		t.Error("cookie for another user accepted")
	}
	other := &Server{cfg: &config.Config{SecretKey: "ffffffffffffffffffffffffffffffff", AdminUser: "gabe"}}
	if _, ok := other.verifyAdmin(v, now); ok {
		t.Error("cookie signed with a different key accepted")
	}
}

func TestLoginLimiter(t *testing.T) {
	l := newLoginLimiter()
	now := time.Now()
	for range maxLoginFailures {
		if !l.allowed("1.2.3.4", now) {
			t.Fatal("blocked too early")
		}
		l.fail("1.2.3.4", now)
	}
	if l.allowed("1.2.3.4", now) {
		t.Error("not blocked after max failures")
	}
	if !l.allowed("5.6.7.8", now) {
		t.Error("other IP blocked")
	}
	if !l.allowed("1.2.3.4", now.Add(loginWindow+time.Second)) {
		t.Error("still blocked after window")
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/admin/about/3":        "/admin/about/3",
		"":                      "/admin/questions/",
		"https://evil.example/": "/admin/questions/",
		"//evil.example/admin/": "/admin/questions/",
		"/about/":               "/admin/questions/",
		`/admin/\evil`:          "/admin/questions/",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderMarkdown(t *testing.T) {
	got, err := renderMarkdown("Inspired by:  \n-An *[artwork](http://example.com)*")
	if err != nil {
		t.Fatal(err)
	}
	want := "<p>Inspired by:<br />\n-An <em><a href=\"http://example.com\">artwork</a></em></p>"
	if got != want {
		t.Errorf("renderMarkdown = %q, want %q", got, want)
	}
}
