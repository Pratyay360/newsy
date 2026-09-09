package main

import (
	"path"
	"regexp"
	"strings"
)

func matchPostPattern(file, pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return isPostFile(file)
	}
	for _, p := range strings.Split(pattern, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if globMatch(p, file) || globMatch(p, path.Base(file)) {
			return true
		}
	}
	return false
}

func filterPostFiles(files []string, pattern string) []string {	
	out := make([]string, 0, len(files))
	for _, f := range files {
		if !isPostFile(f) {
			continue
		}
		if matchPostPattern(f, pattern) {
			out = append(out, f)
		}
	}
	return out
}

// isPostFile reports whether file has a watched post extension.
// Only .md, .markdown, .mdx and .html are watched.
func isPostFile(file string) bool {
	ext := strings.ToLower(path.Ext(strings.TrimSpace(file)))
	switch ext {
	case ".md", ".markdown", ".mdx", ".html":
		return true
	default:
		return false
	}
}

func globMatch(pattern, name string) bool {
	re, err := globToRegexp(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(name)
}

func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	i := 0
	for i < len(pattern) {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i += 2
			} else {
				b.WriteString("[^/]*")
				i++
			}
		case '?':
			b.WriteString("[^/]")
			i++
		case '[', ']', '{', '}', '(', ')', '+', '|', '^', '$', '.', '\\':
			b.WriteString("\\");b.WriteString(string(c))
			i++
		default:
			b.WriteString(string(c))
			i++
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
