//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"resumesearch/internal/adapter/driven/postgres"
	"resumesearch/internal/constants"
	"resumesearch/internal/domain"
)

func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	pool, err := postgres.MigrateAndConnect(ctx, connStr, "../../../../migrations")
	if err != nil {
		t.Fatalf("failed to migrate and connect: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}

// TestMigrateAndConnect_ConcurrentCallersDoNotRace reproduces the exact
// startup shape of docker-compose: `app` and `worker` both call
// MigrateAndConnect against the same fresh database at roughly the same
// time. Without golang-migrate's internal advisory lock, CREATE EXTENSION
// IF NOT EXISTS is not safe under concurrent execution — two sessions can
// both see "does not exist" and race on Postgres's internal
// pg_extension_name_index, and one loses with a duplicate-key error.
func TestMigrateAndConnect_ConcurrentCallersDoNotRace(t *testing.T) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	const callers = 5
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pool, err := postgres.MigrateAndConnect(ctx, connStr, "../../../../migrations")
			errs[i] = err
			if pool != nil {
				pool.Close()
			}
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: MigrateAndConnect failed: %v", i, err)
		}
	}
}

func TestCreateAndGetResume(t *testing.T) {
	cases := []struct {
		name   string
		resume *domain.Resume
	}{
		{
			name: "resume with skills and years",
			resume: &domain.Resume{
				BatchID: "11111111-1111-1111-1111-111111111111", Filename: "jane.pdf", FilePath: "/data/jane.pdf",
				Status: domain.StatusPending, Skills: []string{"go", "postgres"}, YearsExperience: 5,
			},
		},
		{
			name: "resume with nil skills does not violate NOT NULL default",
			resume: &domain.Resume{
				BatchID: "11111111-1111-1111-1111-111111111111", Filename: "empty.pdf", FilePath: "/data/empty.pdf",
				Status: domain.StatusPending,
			},
		},
	}

	pool := setupTestDB(t)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := repo.CreateResume(ctx, tc.resume); err != nil {
				t.Fatalf("CreateResume failed: %v", err)
			}
			if tc.resume.ID == "" {
				t.Fatal("expected ID to be populated after CreateResume")
			}

			got, err := repo.GetByID(ctx, tc.resume.ID)
			if err != nil {
				t.Fatalf("GetByID failed: %v", err)
			}
			if got.Filename != tc.resume.Filename || got.Status != domain.StatusPending {
				t.Errorf("unexpected resume: %+v", got)
			}
		})
	}
}

func TestGetByID_MissingReturnsErrNotFound(t *testing.T) {
	pool := setupTestDB(t)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected domain.ErrNotFound, got %v", err)
	}
}

// TestSaveExtractedFields_ZeroYearsExperienceStoresAsNull locks in the same
// unknown-vs-asserted-zero distinction CreateResume already makes: a
// zero-value YearsExperience from LLM extraction (meaning "couldn't
// determine it") must be stored as SQL NULL, not literal 0, or MinYears
// filtering in Search would wrongly treat "unknown" as "asserted zero
// years." GetByID/GetByBatchID COALESCE this column to 0 for display, so
// this test queries the raw column directly to actually distinguish NULL
// from 0.
func TestSaveExtractedFields_ZeroYearsExperienceStoresAsNull(t *testing.T) {
	pool := setupTestDB(t)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	r := &domain.Resume{BatchID: "55555555-5555-5555-5555-555555555555", Filename: "z.pdf", FilePath: "/z", Status: domain.StatusPending}
	if err := repo.CreateResume(ctx, r); err != nil {
		t.Fatalf("CreateResume failed: %v", err)
	}

	if err := repo.SaveExtractedFields(ctx, r.ID, domain.ExtractedFields{Skills: []string{"go"}, YearsExperience: 0, Location: "Remote"}); err != nil {
		t.Fatalf("SaveExtractedFields failed: %v", err)
	}

	var years *float64
	if err := pool.QueryRow(ctx, "SELECT years_experience FROM resumes WHERE id = $1", r.ID).Scan(&years); err != nil {
		t.Fatalf("query years_experience: %v", err)
	}
	if years != nil {
		t.Errorf("expected years_experience to be NULL for an unextracted/zero value, got %v", *years)
	}
}

func TestSaveRawText(t *testing.T) {
	pool := setupTestDB(t)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	r := &domain.Resume{BatchID: "66666666-6666-6666-6666-666666666666", Filename: "a.pdf", FilePath: "/a", Status: domain.StatusPending}
	if err := repo.CreateResume(ctx, r); err != nil {
		t.Fatalf("CreateResume failed: %v", err)
	}

	if err := repo.SaveRawText(ctx, r.ID, "extracted resume text"); err != nil {
		t.Fatalf("SaveRawText failed: %v", err)
	}

	got, err := repo.GetByID(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got.RawText != "extracted resume text" {
		t.Errorf("got raw text %q, want %q", got.RawText, "extracted resume text")
	}
}

func TestAdvanceStage(t *testing.T) {
	pool := setupTestDB(t)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	r := &domain.Resume{BatchID: "77777777-7777-7777-7777-777777777777", Filename: "a.pdf", FilePath: "/a", Status: domain.StatusPending}
	if err := repo.CreateResume(ctx, r); err != nil {
		t.Fatalf("CreateResume failed: %v", err)
	}

	got, err := repo.GetByID(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got.Stage != domain.StageExtract {
		t.Fatalf("expected a freshly created resume's stage to default to %q, got %q", domain.StageExtract, got.Stage)
	}

	if err := repo.AdvanceStage(ctx, r.ID, domain.StageClassify); err != nil {
		t.Fatalf("AdvanceStage failed: %v", err)
	}

	got, err = repo.GetByID(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got.Stage != domain.StageClassify {
		t.Errorf("got stage %q, want %q", got.Stage, domain.StageClassify)
	}
}

func TestClaimStaleForRedrive(t *testing.T) {
	pool := setupTestDB(t)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	fresh := &domain.Resume{BatchID: "88888888-8888-8888-8888-888888888881", Filename: "fresh.pdf", FilePath: "/fresh", Status: domain.StatusProcessing}
	stale := &domain.Resume{BatchID: "88888888-8888-8888-8888-888888888882", Filename: "stale.pdf", FilePath: "/stale", Status: domain.StatusProcessing}
	done := &domain.Resume{BatchID: "88888888-8888-8888-8888-888888888883", Filename: "done.pdf", FilePath: "/done", Status: domain.StatusDone}
	for _, r := range []*domain.Resume{fresh, stale, done} {
		if err := repo.CreateResume(ctx, r); err != nil {
			t.Fatalf("CreateResume failed: %v", err)
		}
	}

	// Backdate stale's and done's updated_at directly — CreateResume always
	// sets it to now(), so a real staleness gap has to be created by hand
	// in this test rather than by waiting out staleAfter.
	if _, err := pool.Exec(ctx, "UPDATE resumes SET updated_at = now() - interval '1 hour' WHERE id IN ($1, $2)", stale.ID, done.ID); err != nil {
		t.Fatalf("backdate updated_at: %v", err)
	}

	claimed, err := repo.ClaimStaleForRedrive(ctx, 5*time.Minute, 5, 10)
	if err != nil {
		t.Fatalf("ClaimStaleForRedrive failed: %v", err)
	}

	if len(claimed) != 1 || claimed[0].ID != stale.ID {
		t.Fatalf("expected to claim only the stale, non-terminal resume, got %+v", claimed)
	}
	if claimed[0].RedriveCount != 1 {
		t.Errorf("expected the claim itself to increment redrive_count to 1, got %d", claimed[0].RedriveCount)
	}
	if claimed[0].Stage != domain.StageExtract {
		t.Errorf("got stage %q, want %q", claimed[0].Stage, domain.StageExtract)
	}

	// A second call within the same staleAfter window must not re-claim
	// the row it just bumped updated_at on — this is what makes
	// updated_at safe to double as the claim marker across multiple
	// worker replicas' sweepers.
	claimedAgain, err := repo.ClaimStaleForRedrive(ctx, 5*time.Minute, 5, 10)
	if err != nil {
		t.Fatalf("ClaimStaleForRedrive (second call) failed: %v", err)
	}
	if len(claimedAgain) != 0 {
		t.Errorf("expected no rows claimed on an immediate second call, got %+v", claimedAgain)
	}
}

func TestClaimStaleForRedrive_RowsAtMaxRedrivesAreNotReclaimed(t *testing.T) {
	pool := setupTestDB(t)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	r := &domain.Resume{BatchID: "99999999-9999-9999-9999-999999999999", Filename: "poison.pdf", FilePath: "/poison", Status: domain.StatusProcessing}
	if err := repo.CreateResume(ctx, r); err != nil {
		t.Fatalf("CreateResume failed: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE resumes SET updated_at = now() - interval '1 hour', redrive_count = 6 WHERE id = $1", r.ID); err != nil {
		t.Fatalf("backdate updated_at and set redrive_count: %v", err)
	}

	const maxRedrives = 5
	claimed, err := repo.ClaimStaleForRedrive(ctx, 5*time.Minute, maxRedrives, 10)
	if err != nil {
		t.Fatalf("ClaimStaleForRedrive failed: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("expected a resume already past maxRedrives not to be reclaimed, got %+v", claimed)
	}
}

func TestSaveChunks_UpsertOnConflictIsIdempotent(t *testing.T) {
	pool := setupTestDB(t)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	r := &domain.Resume{BatchID: "33333333-3333-3333-3333-333333333333", Filename: "x.pdf", FilePath: "/x", Status: domain.StatusDone}
	if err := repo.CreateResume(ctx, r); err != nil {
		t.Fatalf("CreateResume failed: %v", err)
	}

	dim := constants.EmbeddingDimension
	vec := make([]float32, dim)
	chunk := domain.Chunk{ChunkIndex: 0, ChunkText: "first version", Embedding: vec}

	// Simulate Kafka redelivery: the same chunk is saved twice.
	if err := repo.SaveChunks(ctx, r.ID, []domain.Chunk{chunk}); err != nil {
		t.Fatalf("first SaveChunks failed: %v", err)
	}
	chunk.ChunkText = "second version"
	if err := repo.SaveChunks(ctx, r.ID, []domain.Chunk{chunk}); err != nil {
		t.Fatalf("second SaveChunks (redelivery) failed: %v", err)
	}

	results, err := repo.Search(ctx, vec, domain.SearchFilters{}, 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected exactly 1 result (upsert must not duplicate the chunk row), got %d", len(results))
	}
}

func TestSearch_FiltersAndRanksByBestChunk(t *testing.T) {
	pool := setupTestDB(t)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	near := &domain.Resume{BatchID: "22222222-2222-2222-2222-222222222222", Filename: "near.pdf", FilePath: "/x", Status: domain.StatusDone, Skills: []string{"go", "postgres"}, YearsExperience: 5}
	far := &domain.Resume{BatchID: "22222222-2222-2222-2222-222222222222", Filename: "far.pdf", FilePath: "/y", Status: domain.StatusDone, Skills: []string{"go", "postgres"}, YearsExperience: 5}
	missingSkill := &domain.Resume{BatchID: "22222222-2222-2222-2222-222222222222", Filename: "missing.pdf", FilePath: "/z", Status: domain.StatusDone, Skills: []string{"go"}, YearsExperience: 5}

	for _, r := range []*domain.Resume{near, far, missingSkill} {
		if err := repo.CreateResume(ctx, r); err != nil {
			t.Fatalf("CreateResume failed: %v", err)
		}
	}

	dim := constants.EmbeddingDimension
	queryVec := make([]float32, dim)
	nearVec := make([]float32, dim)
	farVec := make([]float32, dim)
	for i := range queryVec {
		queryVec[i] = 0.1
		nearVec[i] = 0.1 // identical to queryVec -> cosine distance 0
	}
	farVec[0] = 1 // orthogonal-ish to queryVec -> larger cosine distance than nearVec

	if err := repo.SaveChunks(ctx, near.ID, []domain.Chunk{{ChunkIndex: 0, ChunkText: "go postgres backend", Embedding: nearVec}}); err != nil {
		t.Fatalf("SaveChunks(near) failed: %v", err)
	}
	if err := repo.SaveChunks(ctx, far.ID, []domain.Chunk{{ChunkIndex: 0, ChunkText: "go postgres backend", Embedding: farVec}}); err != nil {
		t.Fatalf("SaveChunks(far) failed: %v", err)
	}
	if err := repo.SaveChunks(ctx, missingSkill.ID, []domain.Chunk{{ChunkIndex: 0, ChunkText: "go backend", Embedding: nearVec}}); err != nil {
		t.Fatalf("SaveChunks(missingSkill) failed: %v", err)
	}

	minYears := 3.0

	cases := []struct {
		name        string
		filters     domain.SearchFilters
		wantCount   int
		wantFirstID string
	}{
		{
			name:        "AND skills filter excludes resume missing one required skill, ranks by distance",
			filters:     domain.SearchFilters{RequiredSkills: []string{"go", "postgres"}},
			wantCount:   2,
			wantFirstID: near.ID,
		},
		{
			name:      "min years filter excludes nothing when all meet threshold",
			filters:   domain.SearchFilters{RequiredSkills: []string{"go"}, MinYears: &minYears},
			wantCount: 3,
		},
		{
			name:      "location filter excludes all when none match",
			filters:   domain.SearchFilters{Location: "Nowhere"},
			wantCount: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, err := repo.Search(ctx, queryVec, tc.filters, 10)
			if err != nil {
				t.Fatalf("Search failed: %v", err)
			}
			if len(results) != tc.wantCount {
				t.Fatalf("expected %d results, got %d: %+v", tc.wantCount, len(results), results)
			}
			if tc.wantFirstID != "" && results[0].Resume.ID != tc.wantFirstID {
				t.Errorf("expected %s ranked first, got %s", tc.wantFirstID, results[0].Resume.Filename)
			}
		})
	}
}

func TestListBatches_AggregatesPerStatusCountsNewestFirst(t *testing.T) {
	pool := setupTestDB(t)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	const batchOlder = "44444444-4444-4444-4444-444444444444"
	const batchNewer = "55555555-5555-5555-5555-555555555555"

	createBatch := func(batchID string, statuses ...domain.Status) {
		t.Helper()
		for _, status := range statuses {
			r := &domain.Resume{
				BatchID: batchID, Filename: "r.pdf", FilePath: "/r.pdf", Status: status,
			}
			if err := repo.CreateResume(ctx, r); err != nil {
				t.Fatalf("CreateResume failed: %v", err)
			}
		}
	}

	createBatch(batchOlder, domain.StatusDone, domain.StatusFailed)
	// Sleep so the two batches get distinct created_at values — the
	// aggregation's ORDER BY MIN(created_at) DESC depends on it, and
	// Postgres's now() has enough resolution to separate them across a
	// real network round trip, but not guaranteed within the same tick.
	time.Sleep(10 * time.Millisecond)
	createBatch(batchNewer, domain.StatusPending, domain.StatusPending, domain.StatusProcessing)

	got, err := repo.ListBatches(ctx)
	if err != nil {
		t.Fatalf("ListBatches failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 batches, got %d: %+v", len(got), got)
	}

	newer, older := got[0], got[1]
	if newer.BatchID != batchNewer {
		t.Errorf("expected newest batch %s first, got %s", batchNewer, newer.BatchID)
	}
	if newer.Total != 3 || newer.Pending != 2 || newer.Processing != 1 || newer.Done != 0 || newer.Failed != 0 {
		t.Errorf("unexpected newer batch counts: %+v", newer)
	}
	if older.BatchID != batchOlder {
		t.Errorf("expected older batch %s second, got %s", batchOlder, older.BatchID)
	}
	if older.Total != 2 || older.Done != 1 || older.Failed != 1 || older.Pending != 0 || older.Processing != 0 {
		t.Errorf("unexpected older batch counts: %+v", older)
	}
}

func TestListBatches_NoBatchesReturnsEmptySlice(t *testing.T) {
	pool := setupTestDB(t)
	repo := postgres.NewRepository(pool)
	ctx := context.Background()

	got, err := repo.ListBatches(ctx)
	if err != nil {
		t.Fatalf("ListBatches failed: %v", err)
	}
	if got == nil {
		t.Error("expected non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 batches, got %d: %+v", len(got), got)
	}
}
