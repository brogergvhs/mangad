package generic

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestImageCollectorTrimsAndDedupes(t *testing.T) {
	col := newImageCollector(buildExtRegex([]string{"webp"}), false)
	col.add("\n https://cdn.test/page-001.webp ", -1)
	col.add("https://cdn.test/page-001.webp", -1)

	got := col.Finalize()
	if len(got) != 1 || got[0] != "https://cdn.test/page-001.webp" {
		t.Fatalf("Finalize() = %#v", got)
	}
}

func TestImageCollectorAllowsQueryAfterExtension(t *testing.T) {
	col := newImageCollector(buildExtRegex([]string{"webp"}), false)
	image := "https://cdn.asurascans.com/asura-images/chapters/academys-genius-swordmaster/140/567e52.webp?v=1781373584"
	col.add(image, -1)

	got := col.Finalize()
	if len(got) != 1 || got[0] != image {
		t.Fatalf("Finalize() = %#v", got)
	}
}

func TestImageCollectorSkipsShareAssets(t *testing.T) {
	col := newImageCollector(buildExtRegex([]string{"jpg"}), false)
	col.add("https://zjcdn.mangahere.org/store/manga/29763/030.0/compressed/n000.jpg", -1)
	col.add("http://www.mangatown.com/media/images/fbshare.jpg", -1)

	got := col.Finalize()
	if len(got) != 1 || got[0] != "https://zjcdn.mangahere.org/store/manga/29763/030.0/compressed/n000.jpg" {
		t.Fatalf("Finalize() = %#v", got)
	}
}

func TestImageCollectorSkipsSiteChromeAssets(t *testing.T) {
	col := newImageCollector(buildExtRegex([]string{"png", "webp"}), false)
	col.add("https://www.zazamanga.com/banner/zazamanga-manga-online-official.png", -1)
	col.add("https://comickz.co.uk/images/ads/ori-expand.png", -1)
	col.add("https://cdn4.zinmanga1.com/thumb/tales-of-demons-and-gods.webp", -1)
	col.add("https://cdn1.comicknew.pictures/the-demonic-supreme-sword/covers/488d339c.webp", -1)
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
			<img src="https://80pd.wowpic2.store/i5/bEqPbYfoMT0Gm1HlZjqfoA5s5rEBevqi3R0Vvq7I6y4AiVMhaGDNl_Pk4wkijRuo" data-mangad-page-image="1">
			<img src="https://static.comix.to/poster">
		</body></html>
	`))
	if err != nil {
		t.Fatal(err)
	}
	col := newImageCollector(buildExtRegex([]string{"webp", "jpg"}), false)
	col.ScanIMGTags(doc, "https://comix.to/title/vyd0/7266081-chapter-30")

	got := col.Finalize()
	if len(got) != 1 || !strings.Contains(got[0], "wowpic2.store") {
		t.Fatalf("Finalize() = %#v", got)
	}
}
