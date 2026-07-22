package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	chaptersPkg "github.com/brogergvhs/kaodoku/internal/chapters"
	"github.com/brogergvhs/kaodoku/internal/database"
	"github.com/brogergvhs/kaodoku/internal/library"
	"github.com/brogergvhs/kaodoku/internal/providers"
	"github.com/brogergvhs/kaodoku/internal/service"
)

func do(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("X-API-Key", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAPIV1AuthFlow(t *testing.T) {
	t.Setenv("KAODOKU_ADMIN_USER", "boss")
	t.Setenv("KAODOKU_ADMIN_PASSWORD", "secret123")
	api, closeDB := testAPI(t)
	defer closeDB()

	// meta is public and reports auth is on.
	var meta metaDTO
	rec := do(t, api, http.MethodGet, "/api/v1/meta", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("meta status = %d", rec.Code)
	}
	_ = json.NewDecoder(rec.Body).Decode(&meta)
	if !meta.AuthRequired || meta.APIVersion != 1 {
		t.Fatalf("meta = %+v", meta)
	}

	// wrong password -> 401.
	if rec := do(t, api, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": "boss", "password": "nope"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad login status = %d", rec.Code)
	}

	// login mints a token and returns me.
	rec = do(t, api, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": "boss", "password": "secret123", "device_name": "test"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var login struct {
		Token string `json:"token"`
		Me    meDTO  `json:"me"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&login)
	if login.Token == "" || login.Me.User.Username != "boss" || len(login.Me.Permissions) == 0 {
		t.Fatalf("login body = %+v", login)
	}

	// me requires the token.
	if rec := do(t, api, http.MethodGet, "/api/v1/me", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me without token = %d", rec.Code)
	}
	if rec := do(t, api, http.MethodGet, "/api/v1/me", login.Token, nil); rec.Code != http.StatusOK {
		t.Fatalf("me with token = %d; body=%s", rec.Code, rec.Body.String())
	}

	// revoke, then the token no longer authenticates.
	if rec := do(t, api, http.MethodDelete, "/api/v1/auth/token", login.Token, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d", rec.Code)
	}
	if rec := do(t, api, http.MethodGet, "/api/v1/me", login.Token, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me after revoke = %d, want 401", rec.Code)
	}
}

func TestAPIV1MetaSingleUser(t *testing.T) {
	api, closeDB := testAPI(t) // no KAODOKU_ADMIN_PASSWORD -> single-user
	defer closeDB()
	var meta metaDTO
	rec := do(t, api, http.MethodGet, "/api/v1/meta", "", nil)
	_ = json.NewDecoder(rec.Body).Decode(&meta)
	if meta.AuthRequired {
		t.Fatalf("single-user meta.auth_required = true")
	}
	if rec := do(t, api, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": "x", "password": "y"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("single-user login status = %d, want 400", rec.Code)
	}
}

func TestAPIV1ReaderAndLibrary(t *testing.T) {
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
	if _, err := repo.UpsertChapters(ctx, title.ID, []chaptersPkg.Chapter{{Chapter: providers.Chapter{URL: "https://example.test/ch-1", Label: "1", NumMain: 1}}}); err != nil {
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
	defer closeDB()
	api := New(svc,
		func(context.Context) (service.RunSummary, error) { return service.RunSummary{}, nil },
		func(context.Context, string) (service.SourceVerifyResult, error) { return service.SourceVerifyResult{}, nil },
	)
	tid := strconv.FormatInt(title.ID, 10)
	cid := strconv.FormatInt(chapter.ID, 10)

	var list struct {
		Items      []titleDTO `json:"items"`
		Total      int        `json:"total"`
		NextCursor string     `json:"next_cursor"`
	}
	rec := do(t, api, http.MethodGet, "/api/v1/library", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("library status = %d", rec.Code)
	}
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].DisplayTitle != "Example" {
		t.Fatalf("library = %+v", list)
	}
	if list.Items[0].CoverImage != fmt.Sprintf("/api/v1/covers/%d", title.ID) {
		t.Fatalf("cover_image = %q", list.Items[0].CoverImage)
	}

	var m readerManifestResponse
	rec = do(t, api, http.MethodGet, "/api/v1/reader/titles/"+tid+"/manifest", "", nil)
	_ = json.NewDecoder(rec.Body).Decode(&m)
	if m.MarkBase != "/api/v1/reader/chapters/" {
		t.Fatalf("mark_base = %q", m.MarkBase)
	}
	if len(m.Chapters) == 0 || len(m.Chapters[0].Pages) == 0 || !strings.HasPrefix(m.Chapters[0].Pages[0].URL, "/api/v1/reader/") {
		t.Fatalf("manifest chapters = %+v", m.Chapters)
	}

	var cp chapterProgressDTO
	rec = do(t, api, http.MethodPost, "/api/v1/reader/chapters/"+cid+"/pages", "", map[string]int{"page": 1, "total_pages": 3})
	if rec.Code != http.StatusOK {
		t.Fatalf("mark page status = %d; body=%s", rec.Code, rec.Body.String())
	}
	_ = json.NewDecoder(rec.Body).Decode(&cp)
	if cp.ReadPages != 1 || cp.TotalPages != 3 {
		t.Fatalf("chapter progress = %+v", cp)
	}

	if rec := do(t, api, http.MethodGet, "/api/v1/reader/chapters/"+cid+"/pages/1", "", nil); rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("page image status = %d len = %d", rec.Code, rec.Body.Len())
	}
	if rec := do(t, api, http.MethodGet, "/api/v1/library/999999", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing title status = %d", rec.Code)
	}
}
