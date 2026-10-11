package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
)

// Report statuses.
const (
	StatusScored = "scored"
	StatusFailed = "failed"
)

// DefaultDetailWords is the section length an endpoint needs to count as documented in detail.
const DefaultDetailWords = 40

// PageChecks are the check codes scored on every page; E7 is scored once per run as public endpoint coverage.
var PageChecks = []string{agent.CheckLanguage, agent.CheckCodeInProse, agent.CheckMarkdownHead, agent.CheckPlanningVoice, agent.CheckOutputMarker}

// ErrNoRecording means the run left no recording to score.
var ErrNoRecording = errors.New("no recording")

// Options configure scoring.
type Options struct {
	Endpoints   []Endpoint
	Language    string
	DetailWords int
	Model       string
	MaxOutput   int
	Duration    time.Duration
	Recording   string
}

func (o Options) withDefaults() Options {
	if len(o.Endpoints) == 0 {
		o.Endpoints = FixtureEndpoints
	}
	if o.Language == "" {
		o.Language = "en"
	}
	if o.DetailWords <= 0 {
		o.DetailWords = DefaultDetailWords
	}
	return o
}

// Report is the score of one eval run.
type Report struct {
	Status          string          `json:"status"`
	Reason          string          `json:"reason,omitempty"`
	Pass            bool            `json:"pass"`
	Failures        []string        `json:"failures,omitempty"`
	Model           string          `json:"model"`
	MaxOutput       int             `json:"maxOutput,omitempty"`
	RunID           string          `json:"runId,omitempty"`
	Recording       string          `json:"recording,omitempty"`
	Generation      *api.Generation `json:"generation,omitempty"`
	Pages           []PageScore     `json:"pages"`
	Checks          CheckSummary    `json:"checks"`
	Coverage        Coverage        `json:"coverage"`
	Words           WordStats       `json:"wordsPerEndpoint"`
	Leaks           []Leak          `json:"internalLeaks"`
	Turns           int             `json:"turns"`
	InputTokens     int             `json:"inputTokens"`
	OutputTokens    int             `json:"outputTokens"`
	TotalTokens     int             `json:"totalTokens"`
	DurationSeconds float64         `json:"durationSeconds"`
	CostUSD         float64         `json:"costUsd"`
	Warnings        []string        `json:"warnings,omitempty"`
}

// PageScore is one page the run would write.
type PageScore struct {
	Op         string          `json:"op"`
	Title      string          `json:"title,omitempty"`
	Slug       string          `json:"slug,omitempty"`
	PageID     string          `json:"pageId,omitempty"`
	Blocks     int             `json:"blocks"`
	Words      int             `json:"words"`
	Headings   int             `json:"headings"`
	CodeBlocks int             `json:"codeBlocks"`
	Findings   []agent.Finding `json:"findings,omitempty"`
}

// Name identifies the page in reports.
func (p PageScore) Name() string {
	for _, s := range []string{p.Title, p.Slug, p.PageID} {
		if s != "" {
			return s
		}
	}
	return "(untitled)"
}

// CheckCount is how many scored checks of one code passed.
type CheckCount struct {
	Passed int `json:"passed"`
	Total  int `json:"total"`
}

// CheckSummary is the check pass rate of the run.
type CheckSummary struct {
	Passed   int                   `json:"passed"`
	Total    int                   `json:"total"`
	PassRate float64               `json:"passRate"`
	ByCode   map[string]CheckCount `json:"byCode"`
}

// Coverage is how many public endpoints the pages document.
type Coverage struct {
	Covered     int             `json:"covered"`
	Detailed    int             `json:"detailed"`
	Total       int             `json:"total"`
	DetailWords int             `json:"detailWords"`
	Endpoints   []EndpointScore `json:"endpoints"`
}

// EndpointScore is the coverage of one public endpoint.
type EndpointScore struct {
	Endpoint string `json:"endpoint"`
	Covered  bool   `json:"covered"`
	Page     string `json:"page,omitempty"`
	Words    int    `json:"words"`
	Detailed bool   `json:"detailed"`
}

// WordStats summarises the words of the covered endpoint sections.
type WordStats struct {
	Min    int     `json:"min"`
	Median int     `json:"median"`
	Mean   float64 `json:"mean"`
}

// Leak is an internal endpoint found on a page.
type Leak struct {
	Endpoint string `json:"endpoint"`
	Page     string `json:"page"`
	BlockKey string `json:"blockKey,omitempty"`
}

type block struct {
	key   string
	typ   string
	text  string
	norm  string
	level int
}

var (
	braceParam = regexp.MustCompile(`\{[^{}/\s]*\}`)
	colonParam = regexp.MustCompile(`/:[a-z_][a-z0-9_]*`)
)

func normalize(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "`", ""))
	s = braceParam.ReplaceAllString(s, "{}")
	return colonParam.ReplaceAllString(s, "/{}")
}

const pathEnd = `(?:$|[^a-z0-9/{}_-])`

func endpointPattern(e Endpoint) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^a-z])` + strings.ToLower(e.Method) + `[\s|]+["']?(?:https?://[^\s/]+(?:/v\d+)?)?` + regexp.QuoteMeta(normalize(e.Path)) + pathEnd)
}

func pathPattern(e Endpoint) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(normalize(e.Path)) + pathEnd)
}

func countWords(text string) int {
	return len(strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' && r != '-' }))
}

func contentMap(raw any) map[string]any {
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func headingLevel(content map[string]any) int {
	switch l := content["level"].(type) {
	case float64:
		return int(l)
	case int:
		return l
	case json.Number:
		n, _ := l.Int64()
		return int(n)
	case string:
		var n int
		_, _ = fmt.Sscanf(l, "%d", &n)
		return n
	}
	return 0
}

func pageBlocks(cbs []api.ChangeBlock) []block {
	out := make([]block, 0, len(cbs))
	for _, cb := range cbs {
		content := contentMap(cb.Content)
		text := agent.DraftBlock{Type: cb.Type, Content: content}.Text()
		b := block{key: cb.Key, typ: cb.Type, text: text, norm: normalize(text)}
		if cb.Type == agent.BlockHeading {
			b.level = headingLevel(content)
			if b.level == 0 {
				b.level = 2
			}
		}
		out = append(out, b)
	}
	return out
}

func readerWords(blocks []block) int {
	n := 0
	for _, b := range blocks {
		if b.typ == agent.BlockCode || b.typ == agent.BlockDivider {
			continue
		}
		n += countWords(b.text)
	}
	return n
}

func sectionWords(blocks []block, at int) (int, int) {
	start, level := 0, 0
	for i := at; i >= 0; i-- {
		if blocks[i].typ == agent.BlockHeading {
			start, level = i, blocks[i].level
			break
		}
	}
	end := len(blocks)
	for j := start + 1; j < len(blocks); j++ {
		if blocks[j].typ == agent.BlockHeading && (blocks[at].typ != agent.BlockHeading || level == 0 || blocks[j].level <= level) {
			end = j
			break
		}
	}
	return start, readerWords(blocks[start:end])
}

type page struct {
	score  PageScore
	blocks []block
}

func collectPages(rec *run.Recording, language string) ([]page, []string) {
	guides := false
	for _, p := range rec.Passes {
		if p.Kind == "guides" {
			guides = true
		}
	}
	var pages []page
	var warnings []string
	for _, p := range rec.Passes {
		for _, w := range p.Warnings {
			warnings = append(warnings, p.Name+": "+w)
		}
		if guides && p.Kind != "guides" {
			continue
		}
		for _, ch := range p.Recorded.Changes {
			if ch.Op == api.OpDelete || len(ch.Blocks) == 0 {
				continue
			}
			mode := agent.DraftUpdate
			if ch.Op == api.OpCreate {
				mode = agent.DraftCreate
			}
			blocks := pageBlocks(ch.Blocks)
			ps := PageScore{Op: ch.Op, Title: ch.Title, Slug: ch.Target.Slug, PageID: ch.Target.PageID, Blocks: len(blocks), Words: readerWords(blocks)}
			for _, b := range blocks {
				switch b.typ {
				case agent.BlockHeading:
					ps.Headings++
				case agent.BlockCode:
					ps.CodeBlocks++
				}
			}
			ps.Findings = agent.CheckBlocks(agent.DraftOptions{Mode: mode, Slug: ch.Target.Slug, Language: language}, ch.Blocks)
			pages = append(pages, page{score: ps, blocks: blocks})
		}
	}
	return pages, warnings
}

func scoreChecks(pages []page, coverageComplete bool) CheckSummary {
	sum := CheckSummary{ByCode: map[string]CheckCount{}}
	tally := func(code string, ok bool) {
		c := sum.ByCode[code]
		c.Total++
		sum.Total++
		if ok {
			c.Passed++
			sum.Passed++
		}
		sum.ByCode[code] = c
	}
	for _, p := range pages {
		failed := map[string]bool{}
		for _, f := range agent.Errors(p.score.Findings) {
			failed[f.Code] = true
		}
		for _, code := range PageChecks {
			tally(code, !failed[code])
		}
	}
	tally(agent.CheckCoverage, coverageComplete)
	if sum.Total > 0 {
		sum.PassRate = math.Round(float64(sum.Passed)/float64(sum.Total)*1000) / 1000
	}
	return sum
}

func scoreCoverage(pages []page, endpoints []Endpoint, detailWords int) (Coverage, WordStats) {
	public := Public(endpoints)
	cov := Coverage{Total: len(public), DetailWords: detailWords, Endpoints: []EndpointScore{}}
	type section struct{ page, start int }
	sections := make([]section, len(public))
	shared := map[section]int{}
	for k, e := range public {
		es := EndpointScore{Endpoint: e.String()}
		re := endpointPattern(e)
		byHeading := false
		for pi, p := range pages {
			for i, b := range p.blocks {
				if !re.MatchString(b.norm) {
					continue
				}
				isHeading := b.typ == agent.BlockHeading
				if byHeading && !isHeading {
					continue
				}
				start, w := sectionWords(p.blocks, i)
				if !es.Covered || isHeading && !byHeading || w > es.Words {
					es.Covered, es.Page, es.Words = true, p.score.Name(), w
					sections[k] = section{page: pi, start: start}
					byHeading = byHeading || isHeading
				}
			}
		}
		if es.Covered {
			shared[sections[k]]++
		}
		cov.Endpoints = append(cov.Endpoints, es)
	}
	var words []int
	for k := range cov.Endpoints {
		es := &cov.Endpoints[k]
		if !es.Covered {
			continue
		}
		es.Words /= shared[sections[k]]
		cov.Covered++
		words = append(words, es.Words)
		if es.Words >= detailWords {
			es.Detailed = true
			cov.Detailed++
		}
	}
	return cov, wordStats(words)
}

func wordStats(words []int) WordStats {
	if len(words) == 0 {
		return WordStats{}
	}
	sorted := append([]int(nil), words...)
	sort.Ints(sorted)
	total := 0
	for _, w := range sorted {
		total += w
	}
	return WordStats{Min: sorted[0], Median: sorted[len(sorted)/2], Mean: math.Round(float64(total)/float64(len(sorted))*10) / 10}
}

func findLeaks(pages []page, endpoints []Endpoint) []Leak {
	leaks := []Leak{}
	for _, e := range Internal(endpoints) {
		re := pathPattern(e)
		for _, p := range pages {
			for _, b := range p.blocks {
				if re.MatchString(b.norm) {
					leaks = append(leaks, Leak{Endpoint: e.String(), Page: p.score.Name(), BlockKey: b.key})
				}
			}
		}
	}
	return leaks
}

func base(opts Options) *Report {
	return &Report{
		Model: opts.Model, MaxOutput: opts.MaxOutput, Recording: opts.Recording,
		Pages: []PageScore{}, Leaks: []Leak{}, DurationSeconds: math.Round(opts.Duration.Seconds()*10) / 10,
		Checks:   CheckSummary{ByCode: map[string]CheckCount{}},
		Coverage: Coverage{Total: len(Public(opts.Endpoints)), DetailWords: opts.DetailWords, Endpoints: []EndpointScore{}},
	}
}

// Failed is the report of a run that left nothing to score.
func Failed(opts Options, reason string) *Report {
	opts = opts.withDefaults()
	r := base(opts)
	r.Status, r.Reason = StatusFailed, reason
	r.Failures = []string{reason}
	return r
}

// Score scores a dry-run recording of the eval fixture.
func Score(rec *run.Recording, opts Options) *Report {
	opts = opts.withDefaults()
	r := base(opts)
	r.Status, r.RunID, r.CostUSD = StatusScored, rec.RunID, rec.CostUSD
	for _, p := range rec.Passes {
		if p.Usage != nil {
			r.Turns += p.Usage.Turns
			r.InputTokens += p.Usage.InputTokens
			r.OutputTokens += p.Usage.OutputTokens
		}
		if r.Generation == nil && p.Generation != nil {
			r.Generation = p.Generation
		}
	}
	r.TotalTokens = r.InputTokens + r.OutputTokens
	if r.Model == "" && r.Generation != nil {
		r.Model = r.Generation.Model
	}
	pages, warnings := collectPages(rec, opts.Language)
	r.Warnings = warnings
	if len(pages) == 0 {
		r.Status, r.Reason = StatusFailed, "the recording holds no page change"
		r.Failures = []string{r.Reason}
		return r
	}
	for _, p := range pages {
		r.Pages = append(r.Pages, p.score)
	}
	r.Coverage, r.Words = scoreCoverage(pages, opts.Endpoints, opts.DetailWords)
	r.Checks = scoreChecks(pages, r.Coverage.Covered == r.Coverage.Total)
	r.Leaks = findLeaks(pages, opts.Endpoints)
	r.Failures = acceptance(r)
	r.Pass = len(r.Failures) == 0
	return r
}

func acceptance(r *Report) []string {
	var out []string
	if len(r.Pages) != 1 {
		out = append(out, fmt.Sprintf("%d pages, expected 1", len(r.Pages)))
	}
	if r.Coverage.Covered < r.Coverage.Total {
		out = append(out, fmt.Sprintf("public endpoints covered %d/%d", r.Coverage.Covered, r.Coverage.Total))
	}
	if r.Coverage.Detailed < r.Coverage.Covered {
		out = append(out, fmt.Sprintf("%d covered endpoints have fewer than %d words", r.Coverage.Covered-r.Coverage.Detailed, r.Coverage.DetailWords))
	}
	if len(r.Leaks) > 0 {
		out = append(out, fmt.Sprintf("%d internal endpoint mentions", len(r.Leaks)))
	}
	for _, code := range PageChecks {
		if c := r.Checks.ByCode[code]; c.Passed < c.Total {
			out = append(out, fmt.Sprintf("%s failed on %d page(s)", code, c.Total-c.Passed))
		}
	}
	for _, p := range r.Pages {
		if p.Headings == 0 {
			out = append(out, p.Name()+" has no heading block")
		}
	}
	return out
}

// LoadRecording reads a recording file.
func LoadRecording(path string) (*run.Recording, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var rec run.Recording
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if rec.Version != run.RecordingVersion {
		return nil, fmt.Errorf("%s has recording format %d; the scorer reads %d", path, rec.Version, run.RecordingVersion)
	}
	return &rec, nil
}

// FindRecording returns the newest recording under root written at or after since.
func FindRecording(root string, since time.Time) (string, error) {
	dir := filepath.Join(root, filepath.FromSlash(run.RecordingDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("%w in %s", ErrNoRecording, dir)
	}
	best, bestMod := "", time.Time{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().Before(since) {
			continue
		}
		if best == "" || info.ModTime().After(bestMod) {
			best, bestMod = filepath.Join(dir, e.Name()), info.ModTime()
		}
	}
	if best == "" {
		return "", fmt.Errorf("%w in %s since %s", ErrNoRecording, dir, since.UTC().Format(time.RFC3339))
	}
	return best, nil
}
