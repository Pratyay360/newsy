package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Pratyay360/probot-go"
	"github.com/google/go-github/v88/github"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	defaultIssueTitle = "announcement"
	defaultIssueLabel = "newsletter"
	maxFileChars      = 20000
)

func main() {
	loadDotEnv()

	opts, err := probot.OptionsFromEnv()
	if err != nil {
		log.Fatal().Err(err).Msg("invalid options from environment")
	}

	store, err := openStoreFromEnv()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to postgres")
	}
	if closer, ok := store.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}

	app, err := probot.New(opts)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create app")
	}
	eff := app.Options()

	registerHandlers(app, store)

	fmt.Printf("probot %s listening on http://%s:%d%s\n",
		app.Version(), eff.Host, eff.Port, app.WebhookPath())

	if err := startCombinedServer(app, store, eff.Host, eff.Port); err != nil {
		log.Fatal().Err(err).Msg("server error")
	}
}

func openStoreFromEnv() (Store, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Info().Msg("no DATABASE_URL set; using in-memory store (single-tenant mode)")
		return NewMemoryStore(), nil
	}
	store, err := OpenPostgresStore(dsn)
	if err != nil {
		return nil, err
	}
	log.Info().Msg("connected to postgres; subscription-based routing active")
	return store, nil
}

func startCombinedServer(app *probot.Probot, store Store, host string, port int) error {
	mux := http.NewServeMux()
	mux.Handle("/api/check", checkHandler(store))
	mux.Handle("/api/check/", checkHandler(store))
	webhook := app.WebhookHandler()
	webhookPath := app.WebhookPath()
	if webhookPath == "" {
		webhookPath = "/api/github/webhooks"
	}
	mux.Handle(webhookPath, webhook)
	if webhookPath != "/webhook" {
		mux.Handle("/webhook", webhook)
		mux.Handle("/webhook/", webhook)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("newsy bot is running\n"))
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", host, port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Info().Str("addr", srv.Addr).Msg("starting server (webhook + API + frontend UI)")
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func loadDotEnv() {
	if err := probot.LoadEnvFile(".env"); err != nil && !os.IsNotExist(err) {
		log.Warn().Err(err).Msg("failed to load .env")
	}
	if cwd, err := os.Getwd(); err == nil {
		log.Info().Str("cwd", cwd).Msg(".env is read from and written to this directory")
	}
}

func registerHandlers(app *probot.Probot, store Store) {
	app.On("push", makePushHandler(store))
	app.On("installation", makeInstallationHandler(store))
	app.On("installation_repositories", makeInstallationReposHandler(store))

	app.OnAny(func(ctx *probot.Context) error {
		l := ctx.Log()
		l.Info().
			Str("name", ctx.Name()).
			Str("delivery_id", ctx.ID()).
			Msg("received webhook event")
		return nil
	})

	app.OnError(func(ctx *probot.Context, err error) {
		log.Error().Err(err).Str("event", ctx.Name()).Msg("webhook handler failed")
	})
}

func makePushHandler(store Store) func(ctx *probot.Context) error {
	return func(ctx *probot.Context) error {
		l := ctx.Log()

		pushRepo, err := ctx.Repo()
		if err != nil {
			l.Error().Err(err).Msg("failed to get repository from context")
			return nil
		}

		tenant, ok := resolveTenantConfig(pushRepo, store, l)
		if !ok {
			return nil
		}

		commitSummary, _, addedFiles, err := pushSummary(ctx.Payload())
		if err != nil {
			l.Error().Err(err).Msg("failed to extract info from payload")
			return err
		}
		newPosts := filterPostFiles(addedFiles, tenant.postPattern)
		if len(newPosts) == 0 {
			l.Debug().Msg("push adds no new posts; skipping announcement")
			return nil
		}

		body := buildAnnouncementBody(ctx, pushRepo, commitSummary, newPosts, l)
		return upsertAnnouncementIssue(ctx, pushRepo.Owner, pushRepo.Repo, tenant.issueTitle, tenant.issueLabel, body, l)
	}
}

func makeInstallationHandler(store Store) func(ctx *probot.Context) error {
	return func(ctx *probot.Context) error {
		payload := ctx.Payload()
		action, _ := payload["action"].(string)
		installationID := payloadInstallationID(payload)
		if installationID == 0 || store == nil {
			return nil
		}
		switch action {
		case "created", "unsuspend", "new_permissions_accepted":
			for _, r := range payloadRepoFullNames(payload, "repositories") {
				owner, repo, ok := splitRepo(r)
				if !ok {
					continue
				}
				_, _ = store.Upsert(context.Background(), Subscription{
					SourceOwner: owner, SourceRepo: repo,
					InstallationID: installationID,
				})
			}
		case "deleted", "suspend":
			_ = store.ClearInstallation(context.Background(), installationID)
		}
		return nil
	}
}

func makeInstallationReposHandler(store Store) func(ctx *probot.Context) error {
	return func(ctx *probot.Context) error {
		payload := ctx.Payload()
		action, _ := payload["action"].(string)
		installationID := payloadInstallationID(payload)
		if installationID == 0 || store == nil {
			return nil
		}
		switch action {
		case "added":
			for _, r := range payloadRepoFullNames(payload, "repositories_added") {
				owner, repo, ok := splitRepo(r)
				if !ok {
					continue
				}
				_, _ = store.Upsert(context.Background(), Subscription{
					SourceOwner: owner, SourceRepo: repo,
					InstallationID: installationID,
				})
			}
		case "removed":
			for _, r := range payloadRepoFullNames(payload, "repositories_removed") {
				owner, repo, ok := splitRepo(r)
				if !ok {
					continue
				}
				_, _ = store.Upsert(context.Background(), Subscription{
					SourceOwner: owner, SourceRepo: repo,
					InstallationID: 0,
				})
			}
		}
		return nil
	}
}

func payloadInstallationID(payload map[string]any) int64 {
	inst, _ := payload["installation"].(map[string]any)
	if inst == nil {
		return 0
	}
	switch id := inst["id"].(type) {
	case float64:
		return int64(id)
	case int64:
		return id
	case int:
		return int64(id)
	}
	return 0
}

func payloadRepoFullNames(payload map[string]any, key string) []string {
	items, _ := payload[key].([]any)
	var out []string
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		if full, _ := m["full_name"].(string); full != "" {
			out = append(out, full)
		}
	}
	return out
}

// tenantConfig is the effective per-push routing: which destination repo and
// issue receive the announcement.
type tenantConfig struct {
	destOwner   string
	destRepo    string
	issueTitle  string
	issueLabel  string
	postPattern string
}

func resolveTenantConfig(pushRepo probot.Repo, store Store, l zerolog.Logger) (tenantConfig, bool) {
	if store != nil {
		sub, err := store.Get(context.Background(), pushRepo.Owner, pushRepo.Repo)
		if err == nil && sub.InstallationID != 0 {
			pattern := strings.TrimSpace(sub.PostPattern)
			l.Info().
				Str("source", pushRepo.Owner+"/"+pushRepo.Repo).
				Str("dest", sub.DestOwner+"/"+sub.DestRepo).
				Msg("routing push via subscription")
			return tenantConfig{
				destOwner:   sub.DestOwner,
				destRepo:    sub.DestRepo,
				issueTitle:  sub.IssueTitle,
				issueLabel:  sub.IssueLabel,
				postPattern: pattern,
			}, true
		}
		if err != nil && err != ErrSubscriptionNotFound {
			l.Error().Err(err).Msg("failed to load subscription; skipping push")
			return tenantConfig{}, false
		}
	}
	l.Debug().Str("repo", pushRepo.Owner+"/"+pushRepo.Repo).Msg("push is not installed; skipping")
	return tenantConfig{}, false
}

func buildAnnouncementBody(ctx *probot.Context, pushRepo probot.Repo, commitSummary string, changedFiles []string, l zerolog.Logger) string {
	var b strings.Builder
	b.WriteString(commitSummary)
	appendChangedFilesList(&b, changedFiles)
	appendFileContents(ctx, &b, pushRepo, changedFiles, l)
	return b.String()
}

func appendChangedFilesList(b *strings.Builder, files []string) {
	if len(files) == 0 {
		return
	}
	b.WriteString("\n**New Post:**\n")
	for _, f := range files {
		b.WriteString("- `")
		b.WriteString(f)
		b.WriteString("`\n")
	}
}

func appendFileContents(ctx *probot.Context, b *strings.Builder, pushRepo probot.Repo, files []string, l zerolog.Logger) {
	if len(files) == 0 {
		return
	}
	headSHA := getHeadSHA(ctx.Payload())
	for _, file := range files {
		content, err := fetchFileContent(ctx, pushRepo.Owner, pushRepo.Repo, file, headSHA)
		if err != nil {
			l.Warn().Err(err).Str("file", file).Msg("failed to fetch file content; skipping")
			continue
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		content, truncated := truncateContent(content, maxFileChars)
		content = strings.ReplaceAll(content, "```", "` `` `")

		b.WriteString(file)
		b.WriteString("\n\n")
		b.WriteString(content)
		if truncated {
			b.WriteString("\n... (truncated)\n")
		}
	}
}

func truncateContent(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	return s[:limit], true
}

func upsertAnnouncementIssue(ctx *probot.Context, destOwner, destRepo, issueTitle, issueLabel, body string, l zerolog.Logger) error {
	existingIssue, err := findOpenIssueByLabel(ctx, destOwner, destRepo, issueLabel)
	if err != nil {
		l.Error().Err(err).Msg("failed to search for existing issue")
	}

	if existingIssue != nil {
		return addCommentToExistingIssue(ctx, destOwner, destRepo, existingIssue, body, l)
	}
	return createNewAnnouncementIssue(ctx, destOwner, destRepo, issueTitle, issueLabel, body, l)
}

func addCommentToExistingIssue(ctx *probot.Context, owner, repo string, issue *github.Issue, body string, l zerolog.Logger) error {
	comment, err := createCommentWithLockHandling(ctx, owner, repo, issue, body)
	if err != nil {
		l.Error().Err(err).Msg("failed to add comment")
		return nil
	}
	if comment != nil {
		l.Info().Str("url", comment.GetHTMLURL()).Msg("added comment")
	}
	return nil
}

func createNewAnnouncementIssue(ctx *probot.Context, owner, repo, title, label, body string, l zerolog.Logger) error {
	issue, err := createIssue(ctx, owner, repo, title, label, body)
	if err != nil {
		l.Error().Err(err).Msg("failed to create issue")
		return nil
	}
	if issue != nil {
		l.Info().Str("url", issue.GetHTMLURL()).Msg("created issue")
	}
	return nil
}

func checkLocked(ctx *probot.Context, owner, repo string, issue *github.Issue) (bool, error) {
	if issue == nil || !issue.GetLocked() {
		return false, nil
	}
	if _, err := ctx.GitHub().Issues.Unlock(context.Background(), owner, repo, issue.GetNumber()); err != nil {
		return false, fmt.Errorf("failed to unlock issue #%d: %w", issue.GetNumber(), err)
	}
	l := ctx.Log()
	l.Info().Int("issue", issue.GetNumber()).Msg("unlocked locked issue for commenting")
	return true, nil
}

func lockIssue(ctx *probot.Context, owner, repo string, issue *github.Issue) error {
	var opts *github.LockIssueOptions
	if reason := issue.GetActiveLockReason(); reason != "" {
		opts = &github.LockIssueOptions{LockReason: reason}
	}
	if _, err := ctx.GitHub().Issues.Lock(context.Background(), owner, repo, issue.GetNumber(), opts); err != nil {
		return fmt.Errorf("failed to re-lock issue #%d: %w", issue.GetNumber(), err)
	}
	l := ctx.Log()
	l.Info().Int("issue", issue.GetNumber()).Msg("re-locked issue")
	return nil
}

func createCommentWithLockHandling(ctx *probot.Context, owner, repo string, issue *github.Issue, body string) (*github.IssueComment, error) {
	wasLocked, err := checkLocked(ctx, owner, repo, issue)
	if err != nil {
		return nil, err
	}

	comment, err := createComment(ctx, owner, repo, issue.GetNumber(), body)
	if err != nil {
		if wasLocked {
			if lockErr := lockIssue(ctx, owner, repo, issue); lockErr != nil {
				ll := ctx.Log()
				ll.Warn().Err(lockErr).Int("issue", issue.GetNumber()).Msg("failed to re-lock issue after comment error")
			}
		}
		return nil, err
	}

	if wasLocked {
		if err := lockIssue(ctx, owner, repo, issue); err != nil {
			ll := ctx.Log()
			ll.Warn().Err(err).Int("issue", issue.GetNumber()).Msg("comment added but failed to re-lock issue")
			return comment, err
		}
	}
	return comment, nil
}

func findOpenIssueByLabel(ctx *probot.Context, owner, repo, label string) (*github.Issue, error) {
	l := ctx.Log()

	// First, search by label
	opts := &github.IssueListByRepoOptions{
		State:       "open",
		Labels:      []string{label},
		ListOptions: github.ListOptions{PerPage: 1},
	}
	issues, _, err := ctx.GitHub().Issues.ListByRepo(context.Background(), owner, repo, opts)
	if err != nil {
		return nil, err
	}
	if len(issues) > 0 {
		l.Info().Int("issue", issues[0].GetNumber()).Str("label", label).Msg("found existing issue by label")
		return issues[0], nil
	}

	// Fallback: search by title if no issue found by label
	titleOpts := &github.IssueListByRepoOptions{
		State:       "open",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	allIssues, _, err := ctx.GitHub().Issues.ListByRepo(context.Background(), owner, repo, titleOpts)
	if err != nil {
		return nil, err
	}
	for _, issue := range allIssues {
		if issue.GetTitle() == defaultIssueTitle {
			l.Info().Int("issue", issue.GetNumber()).Msg("found existing issue by title")
			return issue, nil
		}
	}

	l.Debug().Str("label", label).Msg("no existing issue found")
	return nil, nil
}

func createComment(ctx *probot.Context, owner, repo string, issueNumber int, body string) (*github.IssueComment, error) {
	comment, _, err := ctx.GitHub().Issues.CreateComment(context.Background(), owner, repo, issueNumber, &github.IssueComment{
		Body: &body,
	})
	if err != nil {
		return nil, err
	}
	return comment, nil
}

func createIssue(ctx *probot.Context, owner, repo, title, label, body string) (*github.Issue, error) {
	labels := []string{label}
	issueReq := &github.IssueRequest{
		Title:  &title,
		Body:   &body,
		Labels: &labels,
	}
	issue, _, err := ctx.GitHub().Issues.Create(context.Background(), owner, repo, issueReq)
	if err != nil {
		return nil, err
	}
	return issue, nil
}

func fetchFileContent(ctx *probot.Context, owner, repo, path, ref string) (string, error) {
	opts := &github.RepositoryContentGetOptions{Ref: ref}
	file, _, _, err := ctx.GitHub().Repositories.GetContents(context.Background(), owner, repo, path, opts)
	if err != nil {
		return "", err
	}
	if file == nil {
		return "", fmt.Errorf("file not found: %s", path)
	}
	if file.GetType() == "dir" {
		return "", nil
	}
	content, err := file.GetContent()
	if err != nil {
		return "", err
	}
	return content, nil
}

func splitRepo(s string) (owner, repo string, ok bool) {
	owner, repo, found := strings.Cut(strings.TrimSpace(s), "/")
	return owner, repo, found && owner != "" && repo != ""
}

func getHeadSHA(payload map[string]any) string {
	if sha, ok := payload["after"].(string); ok && sha != "" && sha != "0000000000000000000000000000000000000000" {
		return sha
	}
	if head, ok := payload["head_commit"].(map[string]any); ok {
		if id, ok := head["id"].(string); ok && id != "" {
			return id
		}
	}
	return ""
}

func pushSummary(payload map[string]any) (summary string, changedFiles, addedFiles []string, err error) {
	commits, ok := payload["commits"].([]any)
	if !ok || len(commits) == 0 {
		return "", nil, nil, nil
	}

	var sb strings.Builder
	seen := make(map[string]bool)
	addedSeen := make(map[string]bool)

	for _, c := range commits {
		commit, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if msg, ok := commit["message"].(string); ok && strings.TrimSpace(msg) != "" {
			short := strings.SplitN(msg, "\n", 2)[0]
			if author, ok := commit["author"].(map[string]any); ok {
				if name, ok := author["name"].(string); ok && name != "" {
					fmt.Fprintf(&sb, "- %s (%s)\n", short, name)
				} else {
					fmt.Fprintf(&sb, "- %s\n", short)
				}
			} else {
				fmt.Fprintf(&sb, "- %s\n", short)
			}
		}

		for _, field := range []string{"added", "removed", "modified"} {
			if files, ok := commit[field].([]any); ok {
				for _, f := range files {
					name, ok := f.(string)
					if ok && !seen[name] {
						seen[name] = true
						changedFiles = append(changedFiles, name)
					}
					if field == "added" {
						if ok && !addedSeen[name] {
							addedSeen[name] = true
							addedFiles = append(addedFiles, name)
						}
					}
				}
			}
		}
	}
	return sb.String(), changedFiles, addedFiles, nil
}
