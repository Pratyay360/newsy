package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

type Subscription struct {
	SourceOwner    string
	SourceRepo     string
	DestOwner      string
	DestRepo       string
	IssueTitle     string
	IssueLabel     string
	PostPattern    string
	InstallationID int64
}

func (s Subscription) normalized() Subscription {
	s.SourceOwner = strings.ToLower(strings.TrimSpace(s.SourceOwner))
	s.SourceRepo = strings.ToLower(strings.TrimSpace(s.SourceRepo))
	s.DestOwner = strings.TrimSpace(s.DestOwner)
	s.DestRepo = strings.TrimSpace(s.DestRepo)
	s.PostPattern = strings.TrimSpace(s.PostPattern)
	return s
}

func (s Subscription) validate() (Subscription, error) {
	s = s.normalized()
	if s.SourceOwner == "" || s.SourceRepo == "" {
		return s, errors.New("source_owner and source_repo are required")
	}
	if strings.ContainsAny(s.SourceOwner+s.SourceRepo+s.DestOwner+s.DestRepo, "/ \t\n") {
		return s, errors.New("owner/repo names must not contain slashes or whitespace")
	}
	if s.DestOwner == "" {
		s.DestOwner = s.SourceOwner
	}
	if s.DestRepo == "" {
		s.DestRepo = s.SourceRepo
	}
	if s.IssueTitle == "" {
		s.IssueTitle = defaultIssueTitle
	}
	if s.IssueLabel == "" {
		s.IssueLabel = defaultIssueLabel
	}
	return s, nil
}

// ErrSubscriptionNotFound is returned by Store.Get when no row matches.
var ErrSubscriptionNotFound = errors.New("subscription not found")

type Store interface {
	Get(ctx context.Context, sourceOwner, sourceRepo string) (*Subscription, error)
	Upsert(ctx context.Context, sub Subscription) (*Subscription, error)
	Delete(ctx context.Context, sourceOwner, sourceRepo string) error
	List(ctx context.Context, limit, offset int) ([]Subscription, error)
	// ClearInstallation zeroes the installation id on every row recorded
	// under one GitHub App installation (uninstall, suspend, ...).
	// Rows are kept; only values are updated, never deleted.
	ClearInstallation(ctx context.Context, installationID int64) error
}

// schemaDDL creates the subscriptions table plus its supporting objects. It
// is idempotent so it can run on every startup (simple auto-migrate).
const schemaDDL = `
CREATE TABLE IF NOT EXISTS newsy_subscriptions (
	id               BIGSERIAL PRIMARY KEY,
	installation_id  BIGINT NOT NULL DEFAULT 0,
	source_owner     TEXT NOT NULL,
	source_repo      TEXT NOT NULL,
	dest_owner       TEXT NOT NULL,
	dest_repo        TEXT NOT NULL,
	issue_title      TEXT NOT NULL DEFAULT 'announcement',
	issue_label      TEXT NOT NULL DEFAULT 'newsletter',
	created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
	CONSTRAINT newsy_subscriptions_source_unique UNIQUE (source_owner, source_repo),
	CONSTRAINT newsy_subscriptions_owner_nonempty CHECK (char_length(source_owner) > 0),
	CONSTRAINT newsy_subscriptions_repo_nonempty CHECK (char_length(source_repo) > 0)
);
-- Added for the new-post-only trigger; IF NOT EXISTS keeps this safe on
-- databases created before this column existed.
ALTER TABLE newsy_subscriptions
	ADD COLUMN IF NOT EXISTS post_pattern TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_newsy_subscriptions_installation
	ON newsy_subscriptions (installation_id);
CREATE OR REPLACE FUNCTION newsy_touch_updated_at()
RETURNS TRIGGER AS $$
BEGIN
	NEW.updated_at = now();
	RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS trg_newsy_subscriptions_updated_at ON newsy_subscriptions;
CREATE TRIGGER trg_newsy_subscriptions_updated_at
	BEFORE UPDATE ON newsy_subscriptions
	FOR EACH ROW EXECUTE FUNCTION newsy_touch_updated_at();
`

// queryTimeout bounds every store query so a slow database cannot stall
// webhook deliveries.
const queryTimeout = 5 * time.Second

// PostgresStore is a Store backed by PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// OpenPostgresStore connects, verifies the connection and applies the schema.
func OpenPostgresStore(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if _, err := db.ExecContext(ctx, schemaDDL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) Close() error { return s.db.Close() }

func (s *PostgresStore) Get(ctx context.Context, sourceOwner, sourceRepo string) (*Subscription, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	const q = `SELECT source_owner, source_repo, dest_owner, dest_repo,
		issue_title, issue_label, post_pattern, installation_id
		FROM newsy_subscriptions
		WHERE source_owner = $1 AND source_repo = $2`
	var sub Subscription
	err := s.db.QueryRowContext(ctx, q,
		strings.ToLower(strings.TrimSpace(sourceOwner)),
		strings.ToLower(strings.TrimSpace(sourceRepo)),
	).Scan(&sub.SourceOwner, &sub.SourceRepo, &sub.DestOwner, &sub.DestRepo,
		&sub.IssueTitle, &sub.IssueLabel, &sub.PostPattern, &sub.InstallationID)
	if err == sql.ErrNoRows {
		return nil, ErrSubscriptionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	return &sub, nil
}

func (s *PostgresStore) Upsert(ctx context.Context, sub Subscription) (*Subscription, error) {
	v, err := sub.validate()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	const q = `INSERT INTO newsy_subscriptions
		(source_owner, source_repo, dest_owner, dest_repo, issue_title, issue_label, post_pattern, installation_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (source_owner, source_repo) DO UPDATE SET
			dest_owner = EXCLUDED.dest_owner,
			dest_repo = EXCLUDED.dest_repo,
			issue_title = EXCLUDED.issue_title,
			issue_label = EXCLUDED.issue_label,
			post_pattern = EXCLUDED.post_pattern,
			installation_id = EXCLUDED.installation_id
		RETURNING source_owner, source_repo, dest_owner, dest_repo,
			issue_title, issue_label, post_pattern, installation_id`
	var out Subscription
	err = s.db.QueryRowContext(ctx, q,
		v.SourceOwner, v.SourceRepo, v.DestOwner, v.DestRepo,
		v.IssueTitle, v.IssueLabel, v.PostPattern, v.InstallationID,
	).Scan(&out.SourceOwner, &out.SourceRepo, &out.DestOwner, &out.DestRepo,
		&out.IssueTitle, &out.IssueLabel, &out.PostPattern, &out.InstallationID)
	if err != nil {
		return nil, fmt.Errorf("upsert subscription: %w", err)
	}
	return &out, nil
}

func (s *PostgresStore) ClearInstallation(ctx context.Context, installationID int64) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	const q = `UPDATE newsy_subscriptions SET installation_id = 0 WHERE installation_id = $1`
	if _, err := s.db.ExecContext(ctx, q, installationID); err != nil {
		return fmt.Errorf("clear installation: %w", err)
	}
	return nil
}

func (s *PostgresStore) Delete(ctx context.Context, sourceOwner, sourceRepo string) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	const q = `DELETE FROM newsy_subscriptions
		WHERE source_owner = $1 AND source_repo = $2`
	res, err := s.db.ExecContext(ctx, q,
		strings.ToLower(strings.TrimSpace(sourceOwner)),
		strings.ToLower(strings.TrimSpace(sourceRepo)),
	)
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	if n == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

func (s *PostgresStore) List(ctx context.Context, limit, offset int) ([]Subscription, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	const q = `SELECT source_owner, source_repo, dest_owner, dest_repo,
		issue_title, issue_label, post_pattern, installation_id
		FROM newsy_subscriptions
		ORDER BY source_owner, source_repo
		LIMIT $1 OFFSET $2`
	rows, err := s.db.QueryContext(ctx, q, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Subscription
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.SourceOwner, &sub.SourceRepo, &sub.DestOwner,
			&sub.DestRepo, &sub.IssueTitle, &sub.IssueLabel, &sub.PostPattern, &sub.InstallationID); err != nil {
			return nil, fmt.Errorf("list subscriptions: %w", err)
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	return out, nil
}

type MemoryStore struct {
	mu   sync.RWMutex
	rows map[string]Subscription
}

// NewMemoryStore creates an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: make(map[string]Subscription)}
}

func memoryKey(owner, repo string) string {
	return strings.ToLower(strings.TrimSpace(owner)) + "/" +
		strings.ToLower(strings.TrimSpace(repo))
}

func (s *MemoryStore) Get(_ context.Context, sourceOwner, sourceRepo string) (*Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sub, ok := s.rows[memoryKey(sourceOwner, sourceRepo)]
	if !ok {
		return nil, ErrSubscriptionNotFound
	}
	out := sub
	return &out, nil
}

func (s *MemoryStore) Upsert(_ context.Context, sub Subscription) (*Subscription, error) {
	v, err := sub.validate()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[memoryKey(v.SourceOwner, v.SourceRepo)] = v
	out := v
	return &out, nil
}

func (s *MemoryStore) Delete(_ context.Context, sourceOwner, sourceRepo string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[memoryKey(sourceOwner, sourceRepo)]; !ok {
		return ErrSubscriptionNotFound
	}
	delete(s.rows, memoryKey(sourceOwner, sourceRepo))
	return nil
}

func (s *MemoryStore) ClearInstallation(_ context.Context, installationID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, sub := range s.rows {
		if sub.InstallationID == installationID {
			sub.InstallationID = 0
			s.rows[k] = sub
		}
	}
	return nil
}

func (s *MemoryStore) List(_ context.Context, limit, offset int) ([]Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Subscription, 0, len(s.rows))
	for _, sub := range s.rows {
		out = append(out, sub)
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}
