package main

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type postFile struct {
	path    string
	content string
}

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
		if isDraftPath(f) {
			continue
		}
		if matchPostPattern(f, pattern) {
			out = append(out, f)
		}
	}
	return out
}

func isPostFile(file string) bool {
	ext := strings.ToLower(path.Ext(strings.TrimSpace(file)))
	switch ext {
	case ".md", ".markdown", ".mdx", ".html":
		return true
	default:
		return false
	}
}

// isDraftPost returns true if the file path or its frontmatter content indicates
// that the post is a draft (and therefore should not trigger an announcement).
func isDraftPost(file, content string) bool {
	if isDraftPath(file) {
		return true
	}
	return isDraftContent(content)
}

// isDraftPath checks if the file path indicates a draft.
// Conventional directories like _drafts, drafts, or .drafts and filenames like
// *.draft.md or draft.md are considered drafts.
func isDraftPath(file string) bool {
	clean := path.Clean(strings.TrimSpace(file))
	parts := strings.Split(clean, "/")
	for i := 0; i < len(parts)-1; i++ {
		dir := strings.ToLower(parts[i])
		if dir == "_drafts" || dir == "drafts" || dir == ".drafts" {
			return true
		}
	}
	base := strings.ToLower(path.Base(clean))
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "draft" || strings.HasSuffix(stem, ".draft") || strings.HasSuffix(stem, "_draft") {
		return true
	}
	if strings.Contains(base, ".draft.") {
		return true
	}
	return false
}

func isDraftContent(content string) bool {
	s := strings.TrimPrefix(content, "\ufeff")
	s = strings.TrimLeft(s, " \t\r\n")
	if s == "" {
		return false
	}

	if isYAMLFrontmatterStart(s) {
		return checkYAMLFrontmatter(s)
	}
	if isTOMLFrontmatterStart(s) {
		return checkTOMLFrontmatter(s)
	}
	if strings.HasPrefix(s, "{") {
		return checkJSONFrontmatter(s)
	}
	return false
}

func isYAMLFrontmatterStart(s string) bool {
	if s == "---" {
		return true
	}
	return strings.HasPrefix(s, "---\n") || strings.HasPrefix(s, "---\r\n") || strings.HasPrefix(s, "--- ") || strings.HasPrefix(s, "---\t")
}

func isTOMLFrontmatterStart(s string) bool {
	if s == "+++" {
		return true
	}
	return strings.HasPrefix(s, "+++\n") || strings.HasPrefix(s, "+++\r\n") || strings.HasPrefix(s, "+++ ") || strings.HasPrefix(s, "+++\t")
}

func extractDelimitedBlock(s, delim string) (block string, ok bool) {
	idx := strings.IndexByte(s, '\n')
	if idx == -1 {
		return "", false
	}
	rest := s[idx+1:]

	lines := strings.Split(rest, "\n")
	var blockLines []string
	found := false
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		if trimmed == delim || (delim == "---" && trimmed == "...") {
			found = true
			break
		}
		blockLines = append(blockLines, line)
	}
	if !found {
		return "", false
	}
	return strings.Join(blockLines, "\n"), true
}

func checkYAMLFrontmatter(s string) bool {
	block, ok := extractDelimitedBlock(s, "---")
	if !ok {
		return false
	}

	var meta map[string]any
	if err := yaml.Unmarshal([]byte(block), &meta); err == nil && meta != nil {
		for k, v := range meta {
			lk := strings.ToLower(strings.TrimSpace(k))
			if lk == "draft" {
				if b, ok := parseBoolLike(v); ok {
					return b
				}
			}
			if lk == "published" {
				if b, ok := parseBoolLike(v); ok {
					return !b
				}
			}
		}
	}

	// Fallback line-by-line check in case of custom YAML tags or formatting
	return scanFrontmatterLines(block, ":")
}

func checkTOMLFrontmatter(s string) bool {
	block, ok := extractDelimitedBlock(s, "+++")
	if !ok {
		return false
	}
	return scanFrontmatterLines(block, "=")
}

func checkJSONFrontmatter(s string) bool {
	var meta map[string]any
	dec := json.NewDecoder(strings.NewReader(s))
	if err := dec.Decode(&meta); err == nil && meta != nil {
		for k, v := range meta {
			lk := strings.ToLower(strings.TrimSpace(k))
			if lk == "draft" {
				if b, ok := parseBoolLike(v); ok {
					return b
				}
			}
			if lk == "published" {
				if b, ok := parseBoolLike(v); ok {
					return !b
				}
			}
		}
	}
	return false
}

func scanFrontmatterLines(block, sep string) bool {
	lines := strings.Split(block, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, sep, 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if key == "draft" {
			if b, ok := parseBoolLike(val); ok {
				return b
			}
		}
		if key == "published" {
			if b, ok := parseBoolLike(val); ok {
				return !b
			}
		}
	}
	return false
}

func parseBoolLike(v any) (bool, bool) {
	switch val := v.(type) {
	case bool:
		return val, true
	case string:
		s := strings.ToLower(strings.TrimSpace(val))
		switch s {
		case "true", "yes", "1", "on":
			return true, true
		case "false", "no", "0", "off":
			return false, true
		}
	case int:
		switch val {
		case 1:
			return true, true
		case 0:
			return false, true
		}
	case int64:
		switch val {
		case 1:
			return true, true
		case 0:
			return false, true
		}
	case float64:
		switch val {
case 1.0:
			return true, true
		case 0.0:
			return false, true
		}
	}
	return false, false
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
			b.WriteString("\\")
			b.WriteString(string(c))
			i++
		default:
			b.WriteString(string(c))
			i++
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
