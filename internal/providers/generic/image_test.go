package generic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestImageCollectorTrimsAndDedupes(t *testing.T) {
	col := newImageCollector(buildExtRegex([]string{"webp"}), nil)
	col.add("\n https://cdn.test/page-001.webp ", -1)
	col.add("https://cdn.test/page-001.webp", -1)

	got := col.Finalize()
	if len(got) != 1 || got[0] != "https://cdn.test/page-001.webp" {
		t.Fatalf("Finalize() = %#v", got)
	}
}

func TestImageCollectorAllowsQueryAfterExtension(t *testing.T) {
	col := newImageCollector(buildExtRegex([]string{"webp"}), nil)
	image := "https://cdn.asurascans.com/asura-images/chapters/academys-genius-swordmaster/140/567e52.webp?v=1781373584"
	col.add(image, -1)

	got := col.Finalize()
	if len(got) != 1 || got[0] != image {
		t.Fatalf("Finalize() = %#v", got)
	}
}

func TestImageCollectorSkipsShareAssets(t *testing.T) {
	col := newImageCollector(buildExtRegex([]string{"jpg"}), nil)
	col.add("https://zjcdn.mangahere.org/store/manga/29763/030.0/compressed/n000.jpg", -1)
	col.add("http://www.mangatown.com/media/images/fbshare.jpg", -1)

	got := col.Finalize()
	if len(got) != 1 || got[0] != "https://zjcdn.mangahere.org/store/manga/29763/030.0/compressed/n000.jpg" {
		t.Fatalf("Finalize() = %#v", got)
	}
}

func TestImageCollectorDropsForeignDomainBanner(t *testing.T) {
	col := newImageCollector(buildExtRegex([]string{"jpg", "webp"}), nil)
	// A VortexScans-style chapter: a foreign credit banner as page 1, then the
	// real pages on the site's own storage domain.
	col.add("https://storage.mangagalaxy.net/public/upload/2024/07/19/image19.webp", -1)
	for i := 1; i <= 12; i++ {
		col.add(fmt.Sprintf("https://storage.vortexscans.org/upload/series/x/%02d.jpg", i), -1)
	}
	got := col.Finalize()
	if len(got) != 12 {
		t.Fatalf("want 12 pages (foreign banner dropped), got %d: %#v", len(got), got)
	}
	for _, u := range got {
		if strings.Contains(u, "mangagalaxy.net") {
			t.Errorf("foreign-domain banner should be dropped: %s", u)
		}
	}
}

func TestImageCollectorKeepsSubdomainShardedPages(t *testing.T) {
	col := newImageCollector(buildExtRegex([]string{"webp"}), nil)
	// Pages sharded across numbered CDN subdomains of one site, plus one foreign
	// banner. The subdomains share a registrable domain, so all pages survive
	// and only the true outlier is dropped.
	col.add("https://ads.example-cdn.net/promo/credits.webp", -1)
	for i := 1; i <= 12; i++ {
		col.add(fmt.Sprintf("https://cdn%d.zinmanga1.com/series/x/%02d.webp", (i%5)+1, i), -1)
	}
	got := col.Finalize()
	if len(got) != 12 {
		t.Fatalf("want 12 sharded pages kept, got %d: %#v", len(got), got)
	}
	for _, u := range got {
		if strings.Contains(u, "example-cdn.net") {
			t.Errorf("foreign banner should be dropped: %s", u)
		}
	}
}

func TestImageCollectorKeepsGenuineMultiDomainSplit(t *testing.T) {
	col := newImageCollector(buildExtRegex([]string{"webp"}), nil)
	// No single domain dominates (3/3): don't prune — this could be a real
	// chapter split across two CDNs.
	for i := 1; i <= 3; i++ {
		col.add(fmt.Sprintf("https://a-cdn.com/x/%02d.webp", i), -1)
		col.add(fmt.Sprintf("https://b-cdn.net/x/%02d.webp", i), -1)
	}
	if got := col.Finalize(); len(got) != 6 {
		t.Fatalf("want all 6 kept when no domain dominates, got %d: %#v", len(got), got)
	}
}

func TestImageCollectorSkipsSiteChromeAssets(t *testing.T) {
	col := newImageCollector(buildExtRegex([]string{"png", "webp"}), nil)
	col.add("https://www.zazamanga.com/banner/zazamanga-manga-online-official.png", -1)
	col.add("https://comickz.co.uk/images/ads/ori-expand.png", -1)
	col.add("https://cdn4.zinmanga1.com/thumb/tales-of-demons-and-gods.webp", -1)
	col.add("https://cdn1.comicknew.pictures/the-demonic-supreme-sword/covers/488d339c.webp", -1)
	col.add("https://temp.compsci88.com/cover/fallback/title.jpg", -1)
	col.add("https://weebcentral.com/static/images/brand.png", -1)
	col.add("https://www.zazamanga.com/image/background-report.png", -1)
	col.add("https://img-r1.2xstorage.com/the-demonic-supreme-sword/30/0.webp", -1)

	got := col.Finalize()
	if len(got) != 1 || got[0] != "https://img-r1.2xstorage.com/the-demonic-supreme-sword/30/0.webp" {
		t.Fatalf("Finalize() = %#v", got)
	}
}

func TestImageCollectorAllowsMarkedExtensionlessPageImages(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
		<html><body>
			<img src="https://80pd.wowpic2.store/i5/bEqPbYfoMT0Gm1HlZjqfoA5s5rEBevqi3R0Vvq7I6y4AiVMhaGDNl_Pk4wkijRuo" data-kaodoku-page-image="1">
			<img src="https://static.comix.to/poster">
		</body></html>
	`))
	if err != nil {
		t.Fatal(err)
	}
	col := newImageCollector(buildExtRegex([]string{"webp", "jpg"}), nil)
	col.ScanIMGTags(doc, "https://comix.to/title/vyd0/7266081-chapter-30")

	got := col.Finalize()
	if len(got) != 1 || !strings.Contains(got[0], "wowpic2.store") {
		t.Fatalf("Finalize() = %#v", got)
	}
}

func TestScanLooseURLsTruncatesEntityEscapedJSON(t *testing.T) {
	// AsuraScans embeds its page list as entity-escaped JSON; the raw scan of
	// the final entry must stop at &quot; instead of consuming the JSON tail,
	// so it dedups into the real page-1 URL.
	body := `{&quot;pages&quot;:[&quot;https://gg.asuracomic.net/storage/media/112/001.webp?v=1770499638&quot;,` +
		`&quot;https://gg.asuracomic.net/storage/media/112/002.webp?v=1770499638&quot;],&quot;width&quot;:[0,1200]}` +
		` also a JS-string form: "https://gg.asuracomic.net/storage/media/112/003.webp?v=1\",\"width\":[0]"`
	c := newImageCollector(buildExtRegex([]string{"webp"}), nil)
	c.ScanLooseURLs(body)
	got := c.Finalize()
	want := map[string]bool{
		"https://gg.asuracomic.net/storage/media/112/001.webp?v=1770499638": true,
		"https://gg.asuracomic.net/storage/media/112/002.webp?v=1770499638": true,
		"https://gg.asuracomic.net/storage/media/112/003.webp?v=1":          true,
	}
	if len(got) != len(want) {
		t.Fatalf("images = %#v, want 3 clean URLs", got)
	}
	for _, u := range got {
		if !want[u] {
			t.Fatalf("unexpected/malformed URL survived: %q", u)
		}
	}
}
