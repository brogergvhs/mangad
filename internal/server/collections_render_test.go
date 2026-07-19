package server

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/brogergvhs/mangad/internal/catalog"
	"github.com/brogergvhs/mangad/internal/library"
)

// The collection-related fragments must execute against their view structs
// without a field mismatch, across the paths the handlers hit.
func TestCollectionTemplatesRender(t *testing.T) {
	u := &webUI{}
	u.tmpl = template.Must(template.New("").Funcs(u.funcs()).ParseFS(templateFS, "templates/*.html"))
	render := func(name string, v any) string {
		var buf bytes.Buffer
		if err := u.tmpl.ExecuteTemplate(&buf, name, v); err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		return buf.String()
	}

	custom := render("collectionCard", collectionCard{Name: "My shelf", URL: "/collections/view?cid=1", CustomID: 1, Count: 2, Covers: []string{"a.jpg", "b.jpg"}, Stacked: true})
	if !strings.Contains(custom, "/ui/collections/1/delete") {
		t.Error("custom card missing delete action")
	}

	dialog := render("addToCollection", addToCollectionView{
		TitleID: 7,
		Custom:  []collectionOption{{Label: "Shelf", Count: 3, CustomID: 4}},
		Smart:   []collectionOption{{Label: "GitS", Count: 2, SmartKey: "100"}},
	})
	for _, want := range []string{`"cid":"4"`, `"smart":"100"`, "/ui/library/7/collections/add", "Create"} {
		if !strings.Contains(dialog, want) {
			t.Errorf("dialog missing %q in:\n%s", want, dialog)
		}
	}

	manage := render("collectionManage", collectionManageView{ID: 4, Name: "Shelf", Members: []library.Title{{ID: 9, DisplayTitle: "A"}}})
	for _, want := range []string{`"title":"9"`, "/ui/collections/4/remove", "A"} {
		if !strings.Contains(manage, want) {
			t.Errorf("manage missing %q in:\n%s", want, manage)
		}
	}
}

// The list-view actions dropdown and its shared dialog bodies must execute
// against their view data without field mismatches.
func TestTitleActionsTemplatesRender(t *testing.T) {
	u := &webUI{}
	u.tmpl = template.Must(template.New("").Funcs(u.funcs()).ParseFS(templateFS, "templates/*.html"))
	render := func(name string, v any) string {
		var buf bytes.Buffer
		if err := u.tmpl.ExecuteTemplate(&buf, name, v); err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		return buf.String()
	}

	actions := render("libraryTitleActions", map[string]any{
		"Title":     library.Title{ID: 3, SourceURL: "https://x/y", MissingCount: 2},
		"CanManage": true,
	})
	for _, want := range []string{"/ui/library/3/collections", "/ui/library/3/remove-dialog", "/ui/library/3/settings-dialog", "Download missing"} {
		if !strings.Contains(actions, want) {
			t.Errorf("actions missing %q in:\n%s", want, actions)
		}
	}
	if out := render("libraryTitleActions", map[string]any{"Title": library.Title{ID: 3}, "CanManage": false}); strings.TrimSpace(out) != "" {
		t.Errorf("view-only user should get no actions menu, got %q", out)
	}

	rm := render("removeTitleDialog", titleDialogView{Title: library.Title{ID: 5, DisplayTitle: "Berserk"}, AniListConnected: true})
	for _, want := range []string{"Remove Berserk?", "/ui/library/5/remove", "delete_anilist", "lib_title_dialog.close()"} {
		if !strings.Contains(rm, want) {
			t.Errorf("remove dialog missing %q in:\n%s", want, rm)
		}
	}

	settings := render("titleSettingsDialog", titleDialogView{Title: library.Title{ID: 5, RefreshInterval: "6h"}, RefreshEvery: "12h"})
	for _, want := range []string{"/ui/library/5/refresh-interval", "6h", "12h"} {
		if !strings.Contains(settings, want) {
			t.Errorf("settings dialog missing %q in:\n%s", want, settings)
		}
	}
}

// The catalog-manga page shows a not-in-library title's details and a single
// Add to Library action gated on the add permission.
func TestCatalogMangaTemplateRenders(t *testing.T) {
	u := &webUI{}
	u.tmpl = template.Must(template.New("").Funcs(u.funcs()).ParseFS(templateFS, "templates/*.html"))
	render := func(v catalogMangaView) string {
		var buf bytes.Buffer
		if err := u.tmpl.ExecuteTemplate(&buf, "catalogManga", v); err != nil {
			t.Fatalf("render catalogManga: %v", err)
		}
		return buf.String()
	}
	m := catalog.Manga{ProviderID: "42", TitleRomaji: "Berserk", Description: "A dark fantasy.", Status: "FINISHED"}
	out := render(catalogMangaView{Manga: m, CanAdd: true})
	for _, want := range []string{"Berserk", "A dark fantasy.", `"provider_id":"42"`, `"redirect":"1"`, "Add to Library"} {
		if !strings.Contains(out, want) {
			t.Errorf("catalogManga missing %q in:\n%s", want, out)
		}
	}
	if out := render(catalogMangaView{Manga: m, CanAdd: false}); strings.Contains(out, "Add to Library") {
		t.Error("Add to Library button should be hidden without add permission")
	}
}
