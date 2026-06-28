package generic

import "testing"

func TestImageCollectorTrimsAndDedupes(t *testing.T) {
	col := newImageCollector(buildExtRegex([]string{"webp"}), false)
	col.add("\n https://cdn.test/page-001.webp ", -1)
	col.add("https://cdn.test/page-001.webp", -1)

	got := col.Finalize()
	if len(got) != 1 || got[0] != "https://cdn.test/page-001.webp" {
		t.Fatalf("Finalize() = %#v", got)
	}
}
