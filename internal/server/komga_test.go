package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestKomgaAPI(t *testing.T) {
	api, _, titleID, chapterID := opdsTestAPI(t)
	tid := strconv.FormatInt(titleID, 10)
	bid := "c" + strconv.FormatInt(chapterID, 10)

	var me struct {
		ID    string   `json:"id"`
		Roles []string `json:"roles"`
	}
	rec := do(t, api, http.MethodGet, "/komga/api/v2/users/me", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d", rec.Code)
	}
	if err := json.NewDecoder(rec.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	if me.ID == "" || !strings.Contains(strings.Join(me.Roles, ","), "PAGE_STREAMING") {
		t.Fatalf("me = %+v", me)
	}

	var libs []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	rec = do(t, api, http.MethodGet, "/komga/api/v1/libraries", "", nil)
	if err := json.NewDecoder(rec.Body).Decode(&libs); err != nil {
		t.Fatal(err)
	}
	if len(libs) != 1 || libs[0].ID != "1" || libs[0].Name != "Kaodoku" {
		t.Fatalf("libraries = %+v", libs)
	}

	type seriesDto struct {
		ID         string `json:"id"`
		BooksCount int    `json:"booksCount"`
		Metadata   struct {
			Title     string   `json:"title"`
			TitleSort string   `json:"titleSort"`
			Status    string   `json:"status"`
			Genres    []string `json:"genres"`
		} `json:"metadata"`
		BooksMetadata struct {
			Created string `json:"created"`
		} `json:"booksMetadata"`
		BooksReadCount       int `json:"booksReadCount"`
		BooksInProgressCount int `json:"booksInProgressCount"`
	}
	var page struct {
		Content       []seriesDto `json:"content"`
		Last          bool        `json:"last"`
		TotalElements int         `json:"totalElements"`
	}
	rec = do(t, api, http.MethodGet, "/komga/api/v1/series?search=exam&page=0&deleted=false", "", nil)
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Content) != 1 || page.Content[0].ID != tid || !page.Last ||
		page.Content[0].Metadata.Title != "Example" || page.Content[0].Metadata.TitleSort == "" ||
		page.Content[0].Metadata.Genres == nil || page.Content[0].BooksMetadata.Created == "" {
		t.Fatalf("series page = %+v", page)
	}
	if page.Content[0].BooksCount != 1 {
		t.Fatalf("booksCount = %d", page.Content[0].BooksCount)
	}

	rec = do(t, api, http.MethodGet, "/komga/api/v1/series?search=nope", "", nil)
	page.Content = nil
	_ = json.NewDecoder(rec.Body).Decode(&page)
	if len(page.Content) != 0 || page.TotalElements != 0 {
		t.Fatalf("empty search = %+v", page)
	}

	type bookDto struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Media    struct{ PagesCount int }
		Metadata struct {
			Number     string  `json:"number"`
			NumberSort float64 `json:"numberSort"`
		} `json:"metadata"`
		ReadProgress *struct {
			Page      int  `json:"page"`
			Completed bool `json:"completed"`
		} `json:"readProgress"`
	}
	var books struct {
		Content []bookDto `json:"content"`
	}
	rec = do(t, api, http.MethodGet, "/komga/api/v1/series/"+tid+"/books?unpaged=true&media_status=READY&deleted=false", "", nil)
	if err := json.NewDecoder(rec.Body).Decode(&books); err != nil {
		t.Fatal(err)
	}
	if len(books.Content) != 1 || books.Content[0].ID != bid ||
		books.Content[0].Media.PagesCount != 3 || books.Content[0].Metadata.NumberSort != 1 ||
		books.Content[0].ReadProgress != nil {
		t.Fatalf("books = %+v", books)
	}

	var pages []struct {
		Number    int    `json:"number"`
		FileName  string `json:"fileName"`
		MediaType string `json:"mediaType"`
	}
	rec = do(t, api, http.MethodGet, "/komga/api/v1/books/"+bid+"/pages", "", nil)
	if err := json.NewDecoder(rec.Body).Decode(&pages); err != nil {
		t.Fatal(err)
	}
	if len(pages) != 3 || pages[0].Number != 1 || pages[0].FileName != "1.jpg" || pages[0].MediaType != "image/jpeg" {
		t.Fatalf("pages = %+v", pages)
	}

	if rec = do(t, api, http.MethodGet, "/komga/api/v1/books/"+bid+"/pages/2", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("page image = %d", rec.Code)
	}
	if rec = do(t, api, http.MethodGet, "/komga/api/v1/books/"+bid+"/pages/1?zero_based=true", "", nil); rec.Code != http.StatusOK || rec.Body.String() != "two" {
		t.Fatalf("zero_based page = %d %q", rec.Code, rec.Body.String())
	}
	if rec = do(t, api, http.MethodGet, "/komga/api/v1/books/"+bid+"/thumbnail", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("thumbnail = %d", rec.Code)
	}
	if rec = do(t, api, http.MethodGet, "/komga/api/v1/books/"+bid+"/file", "", nil); rec.Code != http.StatusOK ||
		!strings.HasPrefix(rec.Body.String(), "PK") {
		t.Fatalf("file = %d", rec.Code)
	}

	if rec = do(t, api, http.MethodPatch, "/komga/api/v1/books/"+bid+"/read-progress", "", map[string]any{"page": 2}); rec.Code != http.StatusNoContent {
		t.Fatalf("patch page = %d; %s", rec.Code, rec.Body.String())
	}
	var series seriesDto
	rec = do(t, api, http.MethodGet, "/komga/api/v1/series/"+tid, "", nil)
	_ = json.NewDecoder(rec.Body).Decode(&series)
	if series.BooksInProgressCount != 1 || series.BooksReadCount != 0 {
		t.Fatalf("series after page mark = %+v", series)
	}

	if rec = do(t, api, http.MethodPatch, "/komga/api/v1/books/"+bid+"/read-progress", "", map[string]any{"completed": true}); rec.Code != http.StatusNoContent {
		t.Fatalf("patch completed = %d", rec.Code)
	}
	var progress struct {
		BooksCount                   int     `json:"booksCount"`
		BooksReadCount               int     `json:"booksReadCount"`
		LastReadContinuousNumberSort float64 `json:"lastReadContinuousNumberSort"`
		MaxNumberSort                float64 `json:"maxNumberSort"`
	}
	rec = do(t, api, http.MethodGet, "/komga/api/v2/series/"+tid+"/read-progress/tachiyomi", "", nil)
	if err := json.NewDecoder(rec.Body).Decode(&progress); err != nil {
		t.Fatal(err)
	}
	if progress.BooksCount != 1 || progress.BooksReadCount != 1 ||
		progress.LastReadContinuousNumberSort != 1 || progress.MaxNumberSort != 1 {
		t.Fatalf("tachiyomi progress = %+v", progress)
	}

	if rec = do(t, api, http.MethodPut, "/komga/api/v2/series/"+tid+"/read-progress/tachiyomi", "", map[string]any{"lastBookNumberSortRead": 1.0}); rec.Code != http.StatusNoContent {
		t.Fatalf("tachiyomi put = %d", rec.Code)
	}

	if rec = do(t, api, http.MethodDelete, "/komga/api/v1/books/"+bid+"/read-progress", "", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete progress = %d", rec.Code)
	}
	rec = do(t, api, http.MethodGet, "/komga/api/v2/series/"+tid+"/read-progress/tachiyomi", "", nil)
	progress.BooksReadCount = -1
	_ = json.NewDecoder(rec.Body).Decode(&progress)
	if progress.BooksReadCount != 0 {
		t.Fatalf("after unread = %+v", progress)
	}

	if rec = do(t, api, http.MethodPatch, "/komga/api/v1/books/"+bid+"/read-progress", "", map[string]any{"page": 3}); rec.Code != http.StatusNoContent {
		t.Fatalf("patch last page = %d", rec.Code)
	}
	rec = do(t, api, http.MethodGet, "/komga/api/v2/series/"+tid+"/read-progress/tachiyomi", "", nil)
	progress.BooksReadCount = -1
	_ = json.NewDecoder(rec.Body).Decode(&progress)
	if progress.BooksReadCount != 1 {
		t.Fatalf("position completion = %+v", progress)
	}
	if rec = do(t, api, http.MethodDelete, "/komga/api/v1/books/"+bid+"/read-progress", "", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("second delete = %d", rec.Code)
	}

	if rec = do(t, api, http.MethodPut, "/komga/api/v2/series/"+tid+"/read-progress/tachiyomi", "", map[string]any{"lastBookNumberSortRead": 1.0}); rec.Code != http.StatusNoContent {
		t.Fatalf("tachiyomi put mark = %d", rec.Code)
	}
	rec = do(t, api, http.MethodGet, "/komga/api/v2/series/"+tid+"/read-progress/tachiyomi", "", nil)
	progress.BooksReadCount = -1
	_ = json.NewDecoder(rec.Body).Decode(&progress)
	if progress.BooksReadCount != 1 {
		t.Fatalf("tracker put did not mark = %+v", progress)
	}
	if float32(5.7) > float32(5.699999809265137) {
		t.Fatal("float32 quantization must equate Mihon's round-tripped numberSort")
	}

	rec = do(t, api, http.MethodGet, "/komga/api/v1/series?search=empty", "", nil)
	page.Content = nil
	_ = json.NewDecoder(rec.Body).Decode(&page)
	if len(page.Content) != 1 {
		t.Fatalf("empty title lookup = %+v", page)
	}
	rec = do(t, api, http.MethodGet, "/komga/api/v1/series/"+page.Content[0].ID+"/books?unpaged=true", "", nil)
	if body := rec.Body.String(); !strings.Contains(body, `"content":[]`) {
		t.Fatalf("empty series books must be [] not null: %s", body)
	}

	for _, path := range []string{
		"/komga/api/v1/series/latest", "/komga/api/v1/series/new", "/komga/api/v1/series/updated",
	} {
		rec := do(t, api, http.MethodGet, path, "", nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"content":[{`) {
			t.Fatalf("%s = %d %s", path, rec.Code, rec.Body.String()[:120])
		}
	}
	if rec := do(t, api, http.MethodPost, "/komga/api/v1/series/list", "", map[string]any{"fullTextSearch": "exam"}); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"name":"Example"`) {
		t.Fatalf("series/list POST = %d", rec.Code)
	}
	for _, path := range []string{"/komga/api/v1/books/latest", "/komga/api/v1/books/ondeck", "/komga/api/v1/books"} {
		if rec := do(t, api, http.MethodGet, path, "", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s = %d", path, rec.Code)
		}
	}

	for _, path := range []string{
		"/komga/api/v1/collections?unpaged=true", "/komga/api/v1/readlists",
		"/komga/api/v1/genres", "/komga/api/v1/tags", "/komga/api/v1/publishers",
		"/komga/api/v1/authors",
	} {
		if rec := do(t, api, http.MethodGet, path, "", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s = %d", path, rec.Code)
		}
	}
}

func TestKomgaBasicAuth(t *testing.T) {
	t.Setenv("KAODOKU_ADMIN_USER", "boss")
	t.Setenv("KAODOKU_ADMIN_PASSWORD", "secret123")
	api, _, _, _ := opdsTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/komga/api/v1/libraries", nil)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-creds = %d", rec.Code)
	}
	for _, path := range []string{"/komga/api/v1/client-settings/global/list", "/komga/api/v1/oauth2/providers", "/komga/api/v1/claim"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("pre-login %s = %d, must be public", path, rec.Code)
		}
	}
	req = httptest.NewRequest(http.MethodGet, "/komga/api/v1/libraries", nil)
	req.SetBasicAuth("boss", "secret123")
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("basic auth = %d; %s", rec.Code, rec.Body.String())
	}
}
