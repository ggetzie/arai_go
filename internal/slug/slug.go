// Package slug implements a slugify compatible with Django's
// django.utils.text.slugify (allow_unicode=False), so slugs written by the Go
// port match the ones Django already stored in main_generatedq.slug.
package slug

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Make converts s to a URL slug the same way Django does: NFKD-normalize and
// drop non-ASCII, lowercase, remove anything that is not a word character,
// whitespace or hyphen, collapse runs of hyphens and whitespace into a single
// hyphen, and strip leading and trailing hyphens and underscores.
func Make(s string) string {
	var b strings.Builder
	pendingSep := false
	for _, r := range norm.NFKD.String(s) {
		switch {
		case r >= 0x80:
			// Dropped by Django's encode("ascii", "ignore").
			continue
		case isSpace(r) || r == '-':
			pendingSep = true
			continue
		case r >= 'A' && r <= 'Z':
			r += 'a' - 'A'
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_':
		default:
			// Punctuation is removed without separating words.
			continue
		}
		if pendingSep && b.Len() > 0 {
			b.WriteByte('-')
		}
		pendingSep = false
		b.WriteRune(r)
	}
	return strings.Trim(b.String(), "-_")
}

// isSpace matches Python's \s for ASCII input, which includes the
// information separators \x1c-\x1f.
func isSpace(r rune) bool {
	return r == ' ' || (r >= '\t' && r <= '\r') || (r >= 0x1c && r <= 0x1f)
}
