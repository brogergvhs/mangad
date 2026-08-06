package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/brogergvhs/kaodoku/internal/auth"
	chaptersPkg "github.com/brogergvhs/kaodoku/internal/chapters"
	"github.com/brogergvhs/kaodoku/internal/database"
	"github.com/brogergvhs/kaodoku/internal/library"
	"github.com/brogergvhs/kaodoku/internal/providers"
	"github.com/brogergvhs/kaodoku/internal/service"
)

func opdsTestAPI(t *testing.T) (http.Handler, *service.JobService, int64, int64) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kaodoku.db")
	db, err := database.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := library.NewRepository(db)
	title, err := repo.AddTitle(ctx, library.AddTitleParams{SourceURL: "https://example.test/m", DisplayTitle: "Example", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertChapters(ctx, title.ID, []chaptersPkg.Chapter{
		{Chapter: providers.Chapter{URL: "https://example.test/ch-1", Label: "1", NumMain: 1}},
		{Chapter: providers.Chapter{URL: "https://example.test/ch-2", Label: "2", NumMain: 2}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddTitle(ctx, library.AddTitleParams{SourceURL: "https://example.test/empty", DisplayTitle: "Empty", Monitored: true}); err != nil {
		t.Fatal(err)
	}
	chapter, err := repo.GetChapterByLabel(ctx, title.ID, "1")
	if err != nil {
		t.Fatal(err)
	}
	cbz := filepath.Join(t.TempDir(), "c1.cbz")
	writeReaderTestCBZ(t, cbz)
	if err := repo.MarkDownloadCompleted(ctx, chapter.ID, cbz, 100, 3); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	svc, closeDB, err := service.OpenJobs(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeDB)
	api := New(svc,
		func(context.Context) (service.RunSummary, error) { return service.RunSummary{}, nil },
		func(context.Context, string) (service.SourceVerifyResult, error) {
			return service.SourceVerifyResult{}, nil
		},
	)
	return api, svc, title.ID, chapter.ID
}

func TestOPDSCatalog(t *testing.T) {
	api, _, titleID, chapterID := opdsTestAPI(t)
	tid := strconv.FormatInt(titleID, 10)
	cid := strconv.FormatInt(chapterID, 10)

	rec := do(t, api, http.MethodGet, "/opds", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("root status = %d; %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "kind=navigation") {
		t.Fatalf("root content-type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `xmlns="http://www.w3.org/2005/Atom"`) || !strings.Contains(body, "/opds/v1.2/series") {
		t.Fatalf("root feed = %s", body)
	}

	rec = do(t, api, http.MethodGet, "/opds/v1.2/series", "", nil)
	body = rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Example") ||
		!strings.Contains(body, "/opds/v1.2/series/"+tid) ||
		!strings.Contains(body, "/opds/v1.2/covers/"+tid) {
		t.Fatalf("series feed = %d %s", rec.Code, body)
	}

	rec = do(t, api, http.MethodGet, "/opds/v1.2/series/"+tid, "", nil)
	body = rec.Body.String()
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "kind=acquisition") {
		t.Fatalf("title feed content-type = %q", ct)
	}
	if !strings.Contains(body, "/opds/v1.2/thumb/chapters/"+cid) {
		t.Fatalf("chapter thumbnail link missing: %s", body)
	}
	if !strings.Contains(body, "/opds/v1.2/download/chapters/"+cid+".cbz") ||
		!strings.Contains(body, `pse:count="3"`) ||
		!strings.Contains(body, "/opds/v1.2/image/chapters/"+cid+"/{pageNumber}") {
		t.Fatalf("title feed = %s", body)
	}
	if strings.Contains(body, "ch-2") || strings.Contains(body, "Ch 2") {
		t.Fatalf("undownloaded chapter leaked into feed: %s", body)
	}

	rec = do(t, api, http.MethodGet, "/opds/v1.2/download/chapters/"+cid+".cbz", "", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/vnd.comicbook+zip" ||
		!strings.HasPrefix(rec.Body.String(), "PK") {
		t.Fatalf("download = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	rec = do(t, api, http.MethodGet, "/opds/v1.2/image/chapters/"+cid+"/0", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("first PSE page (0-based) status = %d; %s", rec.Code, rec.Body.String())
	}
	rec = do(t, api, http.MethodGet, "/opds/v1.2/image/chapters/"+cid+"/1", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("page status = %d; %s", rec.Code, rec.Body.String())
	}
	rec = do(t, api, http.MethodGet, "/opds/v1.2/series/"+tid+"/chapters", "", nil)
	if body = rec.Body.String(); !strings.Contains(body, `pse:lastRead="2"`) {
		t.Fatalf("progress not reported back: %s", body)
	}
}

func TestOPDSBasicAuth(t *testing.T) {
	t.Setenv("KAODOKU_ADMIN_USER", "boss")
	t.Setenv("KAODOKU_ADMIN_PASSWORD", "secret123")
	api, svc, _, _ := opdsTestAPI(t)

	get := func(user, pass string, withCreds bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/opds", nil)
		if withCreds {
			req.SetBasicAuth(user, pass)
		}
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, req)
		return rec
	}

	rec := get("", "", false)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("no-creds = %d, challenge = %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	if rec := get("boss", "wrong", true); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad-creds = %d", rec.Code)
	}
	if rec := get("boss", "secret123", true); rec.Code != http.StatusOK {
		t.Fatalf("good-creds = %d; %s", rec.Code, rec.Body.String())
	}
	ctx := context.Background()
	roles, err := svc.Auth().ListRoles(ctx)
	if err != nil || len(roles) == 0 {
		t.Fatal(err)
	}
	if err := svc.Auth().CreateUser(ctx, "reader", "bookworm1", roles[0].ID, auth.ContentGuards{AllowAdult: true}); err != nil {
		t.Fatal(err)
	}
	if rec := get("reader", "bookworm1", true); rec.Code != http.StatusOK {
		t.Fatalf("local user = %d; %s", rec.Code, rec.Body.String())
	}
	readerID, err := svc.Auth().Authenticate(ctx, "reader", "bookworm1")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Auth().ChangePassword(ctx, readerID, "bookworm1", "rotated456"); err != nil {
		t.Fatal(err)
	}
	flushBasicCreds()
	if rec := get("reader", "bookworm1", true); rec.Code != http.StatusUnauthorized {
		t.Fatalf("stale creds after password change = %d", rec.Code)
	}
	if rec := get("reader", "rotated456", true); rec.Code != http.StatusOK {
		t.Fatalf("rotated creds = %d", rec.Code)
	}
}
