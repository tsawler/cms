package cms

import (
	"fmt"
	"regexp"
	"strings"
)

// Locale codes are short lowercase language tags ("en", "fr", "pt-br") —
// they double as URL path prefixes, so anything fancier is rejected.
var localeRe = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})?$`)

// validateLocales checks Config.Locales: valid codes, no duplicates. The
// first entry is the default locale and gets no URL prefix.
func validateLocales(locales []string) error {
	seen := map[string]bool{}
	for _, l := range locales {
		if !localeRe.MatchString(l) {
			return fmt.Errorf("cms: invalid locale %q in Config.Locales (want a lowercase tag like \"en\" or \"pt-br\")", l)
		}
		if seen[l] {
			return fmt.Errorf("cms: duplicate locale %q in Config.Locales", l)
		}
		seen[l] = true
	}
	return nil
}

// splitLocalePath resolves the locale for a trimmed request path: a first
// segment matching a configured non-default locale selects it and is
// stripped from the slug. Everything else is the default locale.
func (c *CMS) splitLocalePath(slug string) (locale, rest string) {
	locale = c.cfg.Locales[0]
	if len(c.cfg.Locales) < 2 {
		return locale, slug
	}
	first, tail, _ := strings.Cut(slug, "/")
	for _, l := range c.cfg.Locales[1:] {
		if first == l {
			return l, tail
		}
	}
	return locale, slug
}
