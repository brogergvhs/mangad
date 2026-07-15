package library

import "testing"

func TestParseVolumeFile(t *testing.T) {
	cases := []struct {
		file string
		num  float64
		name string
	}{
		{"Vol. 01 - Naruto Uzumaki.cbz", 1, "Naruto Uzumaki"},
		{"Vol. 53 - Naruto's Birth.cbz", 53, "Naruto's Birth"},
		{"Volume 12.cbz", 12, ""},
		{"vol.7 The Successor.cbz", 7, "The Successor"},
		{"03 - Extras.cbz", 3, "Extras"},
		{"Omake.cbz", 0, "Omake"},
	}
	for _, tc := range cases {
		num, name := ParseVolumeFile(tc.file)
		if num != tc.num || name != tc.name {
			t.Errorf("ParseVolumeFile(%q) = (%v, %q), want (%v, %q)", tc.file, num, name, tc.num, tc.name)
		}
	}
}
