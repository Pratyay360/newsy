package main

import (
	"strings"
	"testing"
)

func TestIsDraftPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"_drafts/post.md", true},
		{"content/_drafts/hello.md", true},
		{"posts/drafts/hello.markdown", true},
		{"posts/.drafts/hello.mdx", true},
		{"posts/my-post.draft.md", true},
		{"posts/my-post_draft.md", true},
		{"posts/draft.md", true},
		{"posts/2026-09-16-hello.md", false},
		{"posts/nfl-draft-2026.md", false},
		{"posts/drafting-an-essay.md", false},
		{"my-drafts.md", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isDraftPath(tt.path)
			if got != tt.want {
				t.Errorf("isDraftPath(%q) = %v; want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsDraftContentYAML(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name: "draft true boolean",
			content: `---
title: "Hello World"
draft: true
---
Body content`,
			want: true,
		},
		{
			name: "draft false boolean",
			content: `---
title: "Hello World"
draft: false
---
Body content`,
			want: false,
		},
		{
			name: "draft true string",
			content: `---
title: "Hello World"
draft: "true"
---
Body content`,
			want: true,
		},
		{
			name: "draft yes",
			content: `---
title: "Hello World"
draft: yes
---
Body content`,
			want: true,
		},
		{
			name: "draft 1 integer",
			content: `---
title: "Hello World"
draft: 1
---
Body content`,
			want: true,
		},
		{
			name: "published false",
			content: `---
title: "Hello World"
published: false
---
Body content`,
			want: true,
		},
		{
			name: "published true",
			content: `---
title: "Hello World"
published: true
---
Body content`,
			want: false,
		},
		{
			name: "no draft field",
			content: `---
title: "Hello World"
date: 2026-09-16
---
Body content`,
			want: false,
		},
		{
			name: "title contains draft word",
			content: `---
title: "How to draft a blog post"
tags:
  - draft
---
Body content with draft mentions`,
			want: false,
		},
		{
			name: "closing delimiter dots",
			content: `---
title: "Hello World"
draft: true
...
Body content`,
			want: true,
		},
		{
			name: "UTF-8 BOM with draft",
			content: "\ufeff---\ntitle: \"Hello\"\ndraft: true\n---\nBody",
			want: true,
		},
		{
			name: "unquoted colon in title fallback",
			content: `---
title: Note: Something
draft: true
---
Body`,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDraftContent(tt.content)
			if got != tt.want {
				t.Errorf("isDraftContent() = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestIsDraftContentTOML(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name: "toml draft true",
			content: `+++
title = "Hello"
draft = true
+++
Body`,
			want: true,
		},
		{
			name: "toml draft false",
			content: `+++
title = "Hello"
draft = false
+++
Body`,
			want: false,
		},
		{
			name: "toml published false",
			content: `+++
title = "Hello"
published = false
+++
Body`,
			want: true,
		},
		{
			name: "toml no draft field",
			content: `+++
title = "Draft essay"
+++
Body`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDraftContent(tt.content)
			if got != tt.want {
				t.Errorf("isDraftContent() = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestIsDraftContentJSON(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "json draft true",
			content: `{"title": "Hello", "draft": true}`,
			want:    true,
		},
		{
			name:    "json draft false",
			content: `{"title": "Hello", "draft": false}`,
			want:    false,
		},
		{
			name:    "json published false",
			content: `{"title": "Hello", "published": false}`,
			want:    true,
		},
		{
			name:    "json without draft",
			content: `{"title": "Draft post"}`,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDraftContent(tt.content)
			if got != tt.want {
				t.Errorf("isDraftContent() = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestIsDraftContentNoFrontmatter(t *testing.T) {
	content := "# My First Post\n\nThis article talks about the draft."
	if isDraftContent(content) {
		t.Errorf("expected isDraftContent to return false for markdown without frontmatter")
	}
}

func TestIsDraftPost(t *testing.T) {
	// Draft by path even if content has no frontmatter
	if !isDraftPost("_drafts/my-post.md", "# Hello") {
		t.Errorf("expected isDraftPost to be true for draft path")
	}

	// Draft by frontmatter even if path looks published
	if !isDraftPost("posts/hello.md", "---\ndraft: true\n---\nContent") {
		t.Errorf("expected isDraftPost to be true for draft frontmatter")
	}

	// Non-draft post
	if isDraftPost("posts/hello.md", "---\ndraft: false\n---\nContent") {
		t.Errorf("expected isDraftPost to be false for non-draft post")
	}
}

func TestFilterPostFilesExcludesDraftPaths(t *testing.T) {
	files := []string{
		"_drafts/draft1.md",
		"posts/drafts/draft2.md",
		"posts/hello.draft.md",
		"posts/hello.md",
		"posts/world.markdown",
		"README.md",
	}

	got := filterPostFiles(files, "")
	expected := []string{"posts/hello.md", "posts/world.markdown", "README.md"}
	if len(got) != len(expected) {
		t.Fatalf("expected %d files, got %d: %v", len(expected), len(got), got)
	}
	for i, f := range expected {
		if got[i] != f {
			t.Errorf("expected got[%d] == %s, got %s", i, f, got[i])
		}
	}
}

func TestBuildAnnouncementBody(t *testing.T) {
	commitSummary := "- Add first post (Alice)\n"
	posts := []postFile{
		{
			path:    "posts/hello.md",
			content: "---\ntitle: Hello\n---\n\nWelcome to my blog!\n```go\nfmt.Println(\"hi\")\n```",
		},
	}

	body := buildAnnouncementBody(commitSummary, posts)
	if !strings.Contains(body, commitSummary) {
		t.Errorf("expected body to contain commit summary")
	}
	if !strings.Contains(body, "**New Post:**\n- `posts/hello.md`") {
		t.Errorf("expected body to contain new post list")
	}
	if !strings.Contains(body, "posts/hello.md\n\n") {
		t.Errorf("expected body to contain post filename header")
	}
	if !strings.Contains(body, "` `` `go") {
		t.Errorf("expected triple backticks to be escaped")
	}
}
