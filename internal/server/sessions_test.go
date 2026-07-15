package server

import "testing"

func TestUADevice(t *testing.T) {
	cases := map[string]string{
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:126.0) Gecko/20100101 Firefox/126.0":                            "Firefox · macOS",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36": "Chrome · Windows",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Safari/604.1":           "Safari · iOS",
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/124.0.0.0 Mobile Safari/537.36":               "Chrome · Android",
		"curl/8.6.0": "curl",
		"":           "Unknown device",
	}
	for ua, want := range cases {
		if got := uaDevice(ua); got != want {
			t.Errorf("uaDevice(%q) = %q, want %q", ua, got, want)
		}
	}
}
