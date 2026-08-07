package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"resumesearch/internal/domain"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) CreateResume(ctx context.Context, res *domain.Resume) error {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO resumes (batch_id, filename, file_path, status, skills, years_experience, location)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at
	`, res.BatchID, res.Filename, res.FilePath, res.Status, nonNilSkills(res.Skills), nullFloat(res.YearsExperience), res.Location)

	if err := row.Scan(&res.ID, &res.CreatedAt, &res.UpdatedAt); err != nil {
		return fmt.Errorf("insert resume: %w", err)
	}
	return nil
}

func (r *Repository) UpdateStatus(ctx context.Context, id string, status domain.Status, errMsg string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE resumes SET status = $1, error_message = $2, updated_at = now() WHERE id = $3
	`, status, errMsg, id)
	if err != nil {
		return fmt.Errorf("update status for resume %s: %w", id, err)
	}
	return nil
}

// AdvanceStage bumps a resume's pipeline stage — see ports.go's
// ResumeRepository.AdvanceStage doc comment for why this is a separate
// write from the stage's own data save, done right after that save
// succeeds and before the stage publishes to the next topic.
func (r *Repository) AdvanceStage(ctx context.Context, id string, stage string) error {
	_, err := r.pool.Exec(ctx, `UPDATE resumes SET stage = $1, updated_at = now() WHERE id = $2`, stage, id)
	if err != nil {
		return fmt.Errorf("advance stage for resume %s: %w", id, err)
	}
	return nil
}

func (r *Repository) SaveRawText(ctx context.Context, id string, rawText string) error {
	_, err := r.pool.Exec(ctx, `UPDATE resumes SET raw_text = $1, updated_at = now() WHERE id = $2`, rawText, id)
	if err != nil {
		return fmt.Errorf("save raw text for resume %s: %w", id, err)
	}
	return nil
}

func (r *Repository) SaveExtractedFields(ctx context.Context, id string, fields domain.ExtractedFields) error {
	extractedJSON, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("marshal extracted fields: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		UPDATE resumes
		SET skills = $1, years_experience = $2, location = $3, extracted_json = $4, updated_at = now()
		WHERE id = $5
	`, nonNilSkills(fields.Skills), nullFloat(fields.YearsExperience), fields.Location, extractedJSON, id)
	if err != nil {
		return fmt.Errorf("save extracted fields for resume %s: %w", id, err)
	}
	return nil
}

func (r *Repository) SaveChunks(ctx context.Context, resumeID string, chunks []domain.Chunk) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op error we intentionally ignore

	for _, c := range chunks {
		_, err := tx.Exec(ctx, `
			INSERT INTO resume_chunks (resume_id, chunk_index, chunk_text, embedding)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (resume_id, chunk_index) DO UPDATE
			SET chunk_text = EXCLUDED.chunk_text, embedding = EXCLUDED.embedding
		`, resumeID, c.ChunkIndex, c.ChunkText, pgvector.NewVector(c.Embedding))
		if err != nil {
			return fmt.Errorf("upsert chunk %d for resume %s: %w", c.ChunkIndex, resumeID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit chunks for resume %s: %w", resumeID, err)
	}
	return nil
}

func (r *Repository) GetByID(ctx context.Context, id string) (domain.Resume, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, batch_id, filename, file_path, status, stage, redrive_count, COALESCE(error_message,''),
		       COALESCE(raw_text,''), skills, COALESCE(years_experience,0), COALESCE(location,''),
		       created_at, updated_at
		FROM resumes WHERE id = $1
	`, id)
	res, err := scanResume(row)
	if err != nil {
		return domain.Resume{}, fmt.Errorf("get resume %s: %w", id, err)
	}
	return res, nil
}

func (r *Repository) GetByBatchID(ctx context.Context, batchID string) ([]domain.Resume, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, batch_id, filename, file_path, status, stage, redrive_count, COALESCE(error_message,''),
		       COALESCE(raw_text,''), skills, COALESCE(years_experience,0), COALESCE(location,''),
		       created_at, updated_at
		FROM resumes WHERE batch_id = $1 ORDER BY created_at
	`, batchID)
	if err != nil {
		return nil, fmt.Errorf("query batch %s: %w", batchID, err)
	}
	defer rows.Close()

	out := make([]domain.Resume, 0)
	for rows.Next() {
		res, err := scanResume(rows)
		if err != nil {
			return nil, fmt.Errorf("scan resume in batch %s: %w", batchID, err)
		}
		out = append(out, res)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate batch %s: %w", batchID, err)
	}
	return out, nil
}

func (r *Repository) Search(ctx context.Context, queryVec []float32, filters domain.SearchFilters, limit int) ([]domain.SearchResult, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT r.id, r.batch_id, r.filename, r.file_path, r.status, r.stage, r.redrive_count, COALESCE(r.error_message,''),
		       COALESCE(r.raw_text,''), r.skills, COALESCE(r.years_experience,0), COALESCE(r.location,''),
		       r.created_at, r.updated_at, MIN(c.embedding <=> $1::vector) AS best_distance
		FROM resumes r
		JOIN resume_chunks c ON c.resume_id = r.id
		WHERE r.status = 'DONE'
		  AND ($2::text[] IS NULL OR r.skills @> $2::text[])
		  AND ($3::double precision IS NULL OR r.years_experience >= $3)
		  AND ($4 = '' OR r.location ILIKE '%' || $4 || '%')
		GROUP BY r.id
		ORDER BY best_distance ASC
		LIMIT $5
	`, pgvector.NewVector(queryVec), nilIfEmpty(filters.RequiredSkills), filters.MinYears, filters.Location, limit)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()

	out := make([]domain.SearchResult, 0)
	for rows.Next() {
		var res domain.Resume
		var distance float32
		if err := rows.Scan(&res.ID, &res.BatchID, &res.Filename, &res.FilePath, &res.Status, &res.Stage, &res.RedriveCount, &res.ErrorMessage,
			&res.RawText, &res.Skills, &res.YearsExperience, &res.Location, &res.CreatedAt, &res.UpdatedAt, &distance); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		out = append(out, domain.SearchResult{Resume: res, BestDistance: distance})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search results: %w", err)
	}
	return out, nil
}

// ClaimStaleForRedrive atomically claims and bumps the redrive_count of up
// to limit resumes that have been sitting at PENDING/PROCESSING with no
// progress for longer than staleAfter — see ports.go's
// ResumeRepository.ClaimStaleForRedrive doc comment for why this must be a
// single atomic UPDATE...RETURNING rather than a separate SELECT then
// UPDATE (multiple worker replicas each run a sweeper, and a SELECT ... FOR
// UPDATE SKIP LOCKED's lock would release before the Kafka publish happens
// outside the transaction, letting two sweepers double-claim the same
// row). updated_at deliberately doubles as the claim marker here — do not
// add a separate "claimed_at" column, this UPDATE's own SET updated_at =
// now() is what keeps a claimed-but-not-yet-redriven row from being
// reclaimed by another sweeper tick within the same staleAfter window.
func (r *Repository) ClaimStaleForRedrive(ctx context.Context, staleAfter time.Duration, maxRedrives, limit int) ([]domain.Resume, error) {
	rows, err := r.pool.Query(ctx, `
		UPDATE resumes SET redrive_count = redrive_count + 1, updated_at = now()
		WHERE id IN (
			SELECT id FROM resumes
			WHERE status IN ('PENDING', 'PROCESSING')
			  AND updated_at < now() - ($1 * INTERVAL '1 second')
			  AND redrive_count <= $2
			LIMIT $3
		)
		RETURNING id, stage, redrive_count
	`, staleAfter.Seconds(), maxRedrives, limit)
	if err != nil {
		return nil, fmt.Errorf("claim stale resumes for redrive: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Resume, 0)
	for rows.Next() {
		var res domain.Resume
		if err := rows.Scan(&res.ID, &res.Stage, &res.RedriveCount); err != nil {
			return nil, fmt.Errorf("scan claimed resume: %w", err)
		}
		out = append(out, res)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimed resumes: %w", err)
	}
	return out, nil
}

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanResume(row rowScanner) (domain.Resume, error) {
	var res domain.Resume
	err := row.Scan(&res.ID, &res.BatchID, &res.Filename, &res.FilePath, &res.Status, &res.Stage, &res.RedriveCount, &res.ErrorMessage,
		&res.RawText, &res.Skills, &res.YearsExperience, &res.Location, &res.CreatedAt, &res.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Resume{}, domain.ErrNotFound
		}
		return domain.Resume{}, err
	}
	return res, nil
}

// nonNilSkills ensures we never bind an explicit SQL NULL for skills: the
// column is `TEXT[] NOT NULL DEFAULT '{}'`, and an explicit NULL parameter
// overrides the column default, tripping the NOT NULL constraint.
func nonNilSkills(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// nullFloat maps the zero value to SQL NULL, distinguishing "unknown years
// of experience" from an asserted zero.
func nullFloat(f float64) any {
	if f == 0 {
		return nil
	}
	return f
}

func nilIfEmpty(s []string) any {
	if len(s) == 0 {
		return nil
	}
	return s
}
