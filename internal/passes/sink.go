package passes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

// Sink receives the writes of a pass: the platform in write mode, a recorder in dry mode.
type Sink interface {
	Dry() bool
	Change(ctx context.Context, req api.ChangeRequest) (*api.Change, error)
	Verbatim(ctx context.Context, req api.VerbatimRequest) (*api.VerbatimResult, error)
	DeleteVerbatim(ctx context.Context, req api.VerbatimDeleteRequest) (*api.VerbatimResult, error)
	Asset(ctx context.Context, repoPath, contentType string, data []byte) (*api.Asset, error)
	Hints(ctx context.Context, hints []api.HintInput) (*api.HintsResult, error)
	Memory(ctx context.Context, req api.MemoryWrite) (*api.MemoryResult, error)
}

// Writer is the platform-facing API of a write run.
type Writer interface {
	ProposeChange(ctx context.Context, runID string, req api.ChangeRequest) (*api.Change, error)
	ImportVerbatim(ctx context.Context, runID string, req api.VerbatimRequest) (*api.VerbatimResult, error)
	DeleteVerbatim(ctx context.Context, runID string, req api.VerbatimDeleteRequest) (*api.VerbatimResult, error)
	UploadAsset(ctx context.Context, runID, runPassID, repoPath, contentType, sha256Hex string, data []byte) (*api.Asset, error)
	RaiseHints(ctx context.Context, runID, runPassID string, hints []api.HintInput) (*api.HintsResult, error)
	WriteMemory(ctx context.Context, req api.MemoryWrite) (*api.MemoryResult, error)
}

// Assets deduplicates uploads by sha256 for the whole run.
type Assets struct {
	mu   sync.Mutex
	urls map[string]*api.Asset
}

// NewAssets returns an empty upload cache.
func NewAssets() *Assets { return &Assets{urls: map[string]*api.Asset{}} }

// PlatformSink writes to the platform inside a run.
type PlatformSink struct {
	W         Writer
	RunID     string
	RunPassID string
	Assets    *Assets
}

// Dry is false: the platform sink writes.
func (s *PlatformSink) Dry() bool { return false }

// Change posts a page change.
func (s *PlatformSink) Change(ctx context.Context, req api.ChangeRequest) (*api.Change, error) {
	req.RunPassID = s.RunPassID
	return s.W.ProposeChange(ctx, s.RunID, req)
}

// Verbatim imports a locked page.
func (s *PlatformSink) Verbatim(ctx context.Context, req api.VerbatimRequest) (*api.VerbatimResult, error) {
	req.RunPassID = s.RunPassID
	return s.W.ImportVerbatim(ctx, s.RunID, req)
}

// DeleteVerbatim proposes deleting a locked page.
func (s *PlatformSink) DeleteVerbatim(ctx context.Context, req api.VerbatimDeleteRequest) (*api.VerbatimResult, error) {
	req.RunPassID = s.RunPassID
	return s.W.DeleteVerbatim(ctx, s.RunID, req)
}

// Asset uploads an image once per run.
func (s *PlatformSink) Asset(ctx context.Context, repoPath, contentType string, data []byte) (*api.Asset, error) {
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])
	if s.Assets != nil {
		s.Assets.mu.Lock()
		if a, ok := s.Assets.urls[key]; ok {
			s.Assets.mu.Unlock()
			return a, nil
		}
		s.Assets.mu.Unlock()
	}
	a, err := s.W.UploadAsset(ctx, s.RunID, s.RunPassID, repoPath, contentType, key, data)
	if err != nil {
		return nil, err
	}
	if s.Assets != nil {
		s.Assets.mu.Lock()
		s.Assets.urls[key] = a
		s.Assets.mu.Unlock()
	}
	return a, nil
}

// Hints raises cross-repository hints.
func (s *PlatformSink) Hints(ctx context.Context, hints []api.HintInput) (*api.HintsResult, error) {
	return s.W.RaiseHints(ctx, s.RunID, s.RunPassID, hints)
}

// Memory writes a Nucleus atom.
func (s *PlatformSink) Memory(ctx context.Context, req api.MemoryWrite) (*api.MemoryResult, error) {
	req.RunID = s.RunID
	return s.W.WriteMemory(ctx, req)
}

// Recorded is what a dry pass would have written.
type Recorded struct {
	Changes   []api.ChangeRequest         `json:"changes,omitempty"`
	Verbatim  []api.VerbatimRequest       `json:"verbatim,omitempty"`
	Deletions []api.VerbatimDeleteRequest `json:"deletions,omitempty"`
	Assets    []string                    `json:"assets,omitempty"`
	Hints     []api.HintInput             `json:"hints,omitempty"`
	Memories  []api.MemoryWrite           `json:"memories,omitempty"`
}

// Recorder is the dry-mode sink: it records would-be writes and writes nothing.
type Recorder struct {
	mu  sync.Mutex
	Rec Recorded
}

// Dry is true: the recorder never writes.
func (r *Recorder) Dry() bool { return true }

// Change records a page change.
func (r *Recorder) Change(_ context.Context, req api.ChangeRequest) (*api.Change, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Rec.Changes = append(r.Rec.Changes, req)
	return &api.Change{Op: req.Op, Status: "dry_run", Page: api.PageRef{ID: req.Target.PageID, Slug: req.Target.Slug, Title: req.Title}}, nil
}

// Verbatim records an import.
func (r *Recorder) Verbatim(_ context.Context, req api.VerbatimRequest) (*api.VerbatimResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Rec.Verbatim = append(r.Rec.Verbatim, req)
	return &api.VerbatimResult{Change: &api.Change{Op: api.OpImport, Status: "dry_run", Page: api.PageRef{Slug: req.Page.Slug, Title: req.Page.Title}}}, nil
}

// DeleteVerbatim records a deletion proposal.
func (r *Recorder) DeleteVerbatim(_ context.Context, req api.VerbatimDeleteRequest) (*api.VerbatimResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Rec.Deletions = append(r.Rec.Deletions, req)
	return &api.VerbatimResult{Change: &api.Change{Op: api.OpDelete, Status: "dry_run"}}, nil
}

// Asset records an upload and returns a placeholder URL naming the repository file.
func (r *Recorder) Asset(_ context.Context, repoPath, _ string, _ []byte) (*api.Asset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Rec.Assets = append(r.Rec.Assets, repoPath)
	return &api.Asset{Key: repoPath, URL: "repo:" + repoPath}, nil
}

// Hints records hints.
func (r *Recorder) Hints(_ context.Context, hints []api.HintInput) (*api.HintsResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Rec.Hints = append(r.Rec.Hints, hints...)
	return &api.HintsResult{}, nil
}

// Memory records an atom.
func (r *Recorder) Memory(_ context.Context, req api.MemoryWrite) (*api.MemoryResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Rec.Memories = append(r.Rec.Memories, req)
	return &api.MemoryResult{Outcome: "dry_run"}, nil
}

// Recorded returns a copy of what the recorder captured.
func (r *Recorder) Recorded() Recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Rec
}
