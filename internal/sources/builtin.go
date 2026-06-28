package sources

import (
	"embed"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed builtin/*.yaml
var builtinFS embed.FS

// BuiltInProfiles returns source profiles bundled with the binary.
func BuiltInProfiles() ([]Profile, error) {
	entries, err := builtinFS.ReadDir("builtin")
	if err != nil {
		return nil, fmt.Errorf("read builtin sources: %w", err)
	}
	out := make([]Profile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		data, err := builtinFS.ReadFile("builtin/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read builtin source %s: %w", entry.Name(), err)
		}
		var profile Profile
		if err := yaml.Unmarshal(data, &profile); err != nil {
			return nil, fmt.Errorf("decode builtin source %s: %w", entry.Name(), err)
		}
		profile, err = normalizeProfile(profile)
		if err != nil {
			return nil, fmt.Errorf("validate builtin source %s: %w", entry.Name(), err)
		}
		out = append(out, profile)
	}
	return out, nil
}
