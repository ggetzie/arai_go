package slug

import "testing"

// Expected values were generated with Django's slugify on the production server.
func TestMakeMatchesDjango(t *testing.T) {
	cases := []struct{ in, want string }{
		{"What is your favorite color?", "what-is-your-favorite-color"},
		{"  Hello   World  ", "hello-world"},
		{"People of Reddit with empty stomach do you ever look at your 🎉 and start masturbating?", "people-of-reddit-with-empty-stomach-do-you-ever-look-at-your-and-start-masturbating"},
		{"Café déjà vu – naïve résumé", "cafe-deja-vu-naive-resume"},
		{"__under_score__", "under_score"},
		{"a--b---c", "a-b-c"},
		{"Tabs\tand\nnewlines", "tabs-and-newlines"},
		{`What's the "best" thing, ever?!`, "whats-the-best-thing-ever"},
		{"1,000 dollars or 50%?", "1000-dollars-or-50"},
		{"Ünïcödé ß æ œ ﬁ", "unicode-fi"},
		{"---", ""},
		{"", ""},
		{"日本語 question", "question"},
		{"x - y _ z", "x-y-_-z"},
		{"People who stick their neck out and yell \"BREAKFAST!\" what are your thoughts on that?\n", "people-who-stick-their-neck-out-and-yell-breakfast-what-are-your-thoughts-on-that"},
	}
	for _, c := range cases {
		if got := Make(c.in); got != c.want {
			t.Errorf("Make(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
