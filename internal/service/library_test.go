package service

import (
	"testing"

	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/library"
)

func TestConfigForTitleOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cfg   *config.Config
		title library.Title
		want  string
	}{
		{
			name: "explicit title output wins",
			cfg:  &config.Config{Output: "Solo_leveling"},
			title: library.Title{
				ID:           2,
				DisplayTitle: "Gachiakuta",
				OutputPath:   "custom/Gachiakuta",
			},
			want: "custom/Gachiakuta",
		},
		{
			name: "default output is title directory",
			cfg:  &config.Config{Output: "Solo_leveling"},
			title: library.Title{
				ID:           2,
				DisplayTitle: "Gachiakuta",
			},
			want: "Gachiakuta",
		},
		{
			name: "display title is path safe",
			cfg:  &config.Config{Output: "."},
			title: library.Title{
				ID:           3,
				DisplayTitle: "Solo Leveling!",
			},
			want: "Solo_Leveling",
		},
		{
			name: "empty title fallback",
			cfg:  &config.Config{Output: "."},
			title: library.Title{
				ID: 4,
			},
			want: "title_4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := configForTitle(tt.cfg, tt.title)
			if got.Output != tt.want {
				t.Fatalf("configForTitle().Output = %q, want %q", got.Output, tt.want)
			}
			if tt.cfg.Output == "" {
				return
			}
			if tt.cfg.Output != "." && tt.title.OutputPath == "" && got.Output == tt.cfg.Output {
				t.Fatalf("configForTitle reused active config output %q", tt.cfg.Output)
			}
		})
	}
}
