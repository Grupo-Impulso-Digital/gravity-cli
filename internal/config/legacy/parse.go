package legacy

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	yaml "go.yaml.in/yaml/v3"
)

// Parse strictly decodes a v1 manifest; unknown keys are errors.
func Parse(data []byte) (*Project, error) {
	var p Project
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse v1 manifest: %w", err)
	}
	return &p, nil
}

// DeadKeys lists the v1 keys that were never read and are dropped by conversion.
func (p *Project) DeadKeys() []string {
	var out []string
	for i, s := range p.Sources {
		if s.Generator != "" {
			out = append(out, fmt.Sprintf("sources[%d].generator", i))
		}
	}
	if p.ReleaseNotes.Changelog != "" {
		out = append(out, "releaseNotes.changelog")
	}
	if p.Knowledge.Scope != "" {
		out = append(out, "knowledge.scope")
	}
	return out
}
