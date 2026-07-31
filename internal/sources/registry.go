package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/brogergvhs/kaodoku/internal/util"
	"gopkg.in/yaml.v3"
)

// Registry is the shareable document format for source profiles.
type Registry struct {
	Profiles []Profile `json:"profiles" yaml:"profiles"`
}

// DecodeProfile decodes one YAML (or JSON, a YAML subset) source profile.
func DecodeProfile(body []byte) (Profile, error) {
	var profile Profile
	if err := yaml.Unmarshal(body, &profile); err != nil {
		return Profile{}, fmt.Errorf("decode source profile: %w", err)
	}
	return normalizeProfile(profile)
}

// EncodeProfileYAML encodes a source profile as shareable YAML.
func EncodeProfileYAML(profile Profile) ([]byte, error) {
	profile, err := normalizeProfile(profile)
	if err != nil {
		return nil, err
	}
	body, err := yaml.Marshal(profile)
	if err != nil {
		return nil, fmt.Errorf("encode source profile: %w", err)
	}
	return body, nil
}

var registryClient, _ = util.NewHTTPClient(util.HTTPClientOptions{Timeout: 15 * time.Second, BlockPrivateNetworks: true})

// FetchRegistry downloads a JSON or YAML profile registry.
func FetchRegistry(ctx context.Context, registryURL string) ([]Profile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, registryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create registry request: %w", err)
	}
	resp, err := registryClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch registry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("fetch registry: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	return DecodeRegistry(body)
}

// DecodeRegistry decodes a registry document. Invalid entries are skipped
// with a warning so one bad upstream profile cannot block all the others.
func DecodeRegistry(body []byte) ([]Profile, error) {
	var reg Registry
	if err := decodeRegistry(body, &reg); err != nil {
		return nil, err
	}
	out := make([]Profile, 0, len(reg.Profiles))
	var lastErr error
	for _, profile := range reg.Profiles {
		p, err := normalizeProfile(profile)
		if err != nil {
			log.Printf("skipping invalid registry profile %q: %v", profile.ID, err)
			lastErr = err
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 && lastErr != nil {
		return nil, fmt.Errorf("registry has no valid profiles: %w", lastErr)
	}
	return out, nil
}

func decodeRegistry(body []byte, reg *Registry) error {
	if err := json.Unmarshal(body, reg); err == nil && len(reg.Profiles) > 0 {
		return nil
	}
	var list []Profile
	if err := json.Unmarshal(body, &list); err == nil && len(list) > 0 {
		reg.Profiles = list
		return nil
	}
	if err := yaml.NewDecoder(bytes.NewReader(body)).Decode(reg); err == nil && len(reg.Profiles) > 0 {
		return nil
	}
	if err := yaml.NewDecoder(bytes.NewReader(body)).Decode(&list); err == nil && len(list) > 0 {
		reg.Profiles = list
		return nil
	}
	if strings.TrimSpace(string(body)) == "" {
		return fmt.Errorf("empty registry")
	}
	return fmt.Errorf("registry has no profiles")
}
