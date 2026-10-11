package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

// Draft modes, one per writer task kind.
const (
	DraftCreate         = "create"
	DraftUpdate         = "update"
	DraftDeprecate      = "deprecate"
	DraftReach          = "reach"
	DraftReferenceProse = "reference-prose"
)

// Draft block origins.
const (
	OriginSeed = "seed"
	OriginNew  = "new"
)

// Key prefixes of new draft blocks.
const (
	KeyPrefixGuide = "guide"
	KeyPrefixRef   = "ref"
)

// AfterFirst places new blocks at the top of the page.
const AfterFirst = "^"

const (
	maxKeyLen = 128
	keyDigits = 7
)

// MustCover is one item a page must cover; a token is machine-checked, the text is advisory.
type MustCover struct {
	Token string `json:"token,omitempty"`
	Text  string `json:"text"`
}

// Uncovered declares a must-cover token the page leaves out, with the reason.
type Uncovered struct {
	Token  string `json:"token"`
	Reason string `json:"reason"`
}

// DraftOptions configure a page draft workspace.
type DraftOptions struct {
	Mode       string
	Slug       string
	KeyPrefix  string
	Contract   api.BlockContract
	Audiences  []string
	ReachUnits []string
	Language   string
	Markers    []string
	MustCover  []MustCover
	Path       string
}

// DraftBlock is one block of a page draft.
type DraftBlock struct {
	Key        string         `json:"key"`
	Type       string         `json:"type"`
	Content    map[string]any `json:"content"`
	Audiences  []string       `json:"audiences,omitempty"`
	Units      []string       `json:"units,omitempty"`
	Rationale  *Rationale     `json:"rationale,omitempty"`
	Locked     bool           `json:"locked"`
	LockReason string         `json:"lockReason,omitempty"`
	Origin     string         `json:"origin"`
}

// Text is the reader-facing text of the block.
func (b DraftBlock) Text() string {
	return blockText(b.Type, b.Content)
}

// Draft is the page workspace the write phases edit through the draft tools.
type Draft struct {
	opts      DraftOptions
	blocks    []*DraftBlock
	seed      map[string]string
	deleted   []string
	next      int
	hints     []HintDraft
	summary   string
	uncovered []Uncovered
	submitted bool
}

// DraftUpsert is one new or changed block of a draft diff, with the key it follows.
type DraftUpsert struct {
	Block DraftBlock
	After string
}

// DraftDiff is what a submitted draft changes on its page.
type DraftDiff struct {
	Upserts []DraftUpsert
	Remove  []string
}

// Empty reports a draft that changes nothing.
func (d DraftDiff) Empty() bool {
	return len(d.Upserts) == 0 && len(d.Remove) == 0
}

func contentMap(raw any) map[string]any {
	var data []byte
	switch v := raw.(type) {
	case nil:
		return map[string]any{}
	case map[string]any:
		data, _ = json.Marshal(v)
	case json.RawMessage:
		data = v
	case []byte:
		data = v
	default:
		data, _ = json.Marshal(v)
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func intersects(a, b []string) bool {
	for _, x := range a {
		if oneOf(x, b) {
			return true
		}
	}
	return false
}

func lockReason(blk api.PageBlock, opts DraftOptions, contract api.BlockContract) string {
	switch {
	case blk.Key == "":
		return "it has no key"
	case blk.Ownership == api.OwnershipHuman:
		return "a person owns it"
	case !oneOf(blk.Type, contract.Types):
		return "its type " + blk.Type + " is not authored"
	case blk.SourceBinding != nil && (blk.SourceBinding.Kind == "api" || blk.SourceBinding.Kind == "endpoint" || blk.SourceBinding.Kind == "verbatim"):
		return "it is generated from " + blk.SourceBinding.Kind
	case blk.Ownership == api.OwnershipHybrid && (blk.Clean == nil || !*blk.Clean):
		return "a person edited it"
	case opts.Mode == DraftReach && !intersects(blk.Units, opts.ReachUnits):
		return "it is not bound to " + strings.Join(opts.ReachUnits, ", ")
	}
	return ""
}

func cutSlug(prefix, slug string) string {
	limit := maxKeyLen - len(prefix) - 2 - keyDigits
	if limit > 0 && len(slug) > limit {
		return slug[:limit]
	}
	return slug
}

// NewDraft seeds a draft from the current page (nil for a new page) and computed locked blocks merged into it.
func NewDraft(opts DraftOptions, page *api.PageContent, computed []api.ChangeBlock) *Draft {
	opts.Contract = blockContract(opts.Contract)
	if opts.KeyPrefix == "" {
		opts.KeyPrefix = KeyPrefixGuide
	}
	if len(opts.Markers) == 0 {
		opts.Markers = DefaultOutputMarkers
	}
	d := &Draft{opts: opts, seed: map[string]string{}}
	index := map[string]*DraftBlock{}
	var keys []string
	if page != nil {
		for _, blk := range page.Blocks {
			content := contentMap(blk.Content)
			if len(content) == 0 && blk.Text != "" {
				content = map[string]any{"text": blk.Text}
			}
			db := &DraftBlock{
				Key: blk.Key, Type: blk.Type, Content: content, Audiences: blk.Audiences, Units: blk.Units, Origin: OriginSeed,
			}
			if reason := lockReason(blk, opts, opts.Contract); reason != "" {
				db.Locked, db.LockReason = true, reason
			} else {
				d.seed[db.Key] = fingerprint(db)
			}
			d.blocks = append(d.blocks, db)
			if blk.Key != "" {
				index[blk.Key] = db
				keys = append(keys, blk.Key)
			}
		}
	}
	for _, cb := range computed {
		content := contentMap(cb.Content)
		if db, ok := index[cb.Key]; ok {
			db.Type, db.Content, db.Units = cb.Type, content, cb.Units
			db.Locked, db.LockReason = true, "it is generated from the API description"
			delete(d.seed, cb.Key)
			continue
		}
		db := &DraftBlock{Key: cb.Key, Type: cb.Type, Content: content, Audiences: cb.Audiences, Units: cb.Units, Locked: true, LockReason: "it is generated from the API description", Origin: OriginSeed}
		d.blocks = append(d.blocks, db)
		index[cb.Key] = db
		keys = append(keys, cb.Key)
	}
	pattern := regexp.MustCompile("^" + regexp.QuoteMeta(opts.KeyPrefix+":"+cutSlug(opts.KeyPrefix, opts.Slug)) + `:(\d+)$`)
	for _, k := range keys {
		if m := pattern.FindStringSubmatch(k); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > d.next {
				d.next = n
			}
		}
	}
	d.save()
	return d
}

// Mode is the draft's writer mode.
func (d *Draft) Mode() string { return d.opts.Mode }

// Blocks returns a copy of the draft blocks in page order.
func (d *Draft) Blocks() []DraftBlock {
	out := make([]DraftBlock, 0, len(d.blocks))
	for _, b := range d.blocks {
		out = append(out, *b)
	}
	return out
}

// EditableKeys lists the keys of the seed blocks the model may change.
func (d *Draft) EditableKeys() []string {
	var out []string
	for _, b := range d.blocks {
		if !b.Locked && b.Origin == OriginSeed {
			out = append(out, b.Key)
		}
	}
	return out
}

// AllEditable reports a draft whose seed blocks are all editable.
func (d *Draft) AllEditable() bool {
	for _, b := range d.blocks {
		if b.Locked {
			return false
		}
	}
	return true
}

// Hints returns the hints raised during the task.
func (d *Draft) Hints() []HintDraft { return append([]HintDraft(nil), d.hints...) }

// Summary is the reviewer summary given at submit.
func (d *Draft) Summary() string { return d.summary }

// Uncovered returns the must-cover tokens declared uncovered at submit.
func (d *Draft) Uncovered() []Uncovered { return append([]Uncovered(nil), d.uncovered...) }

// Submitted reports a draft whose submit_page succeeded.
func (d *Draft) Submitted() bool { return d.submitted }

func (d *Draft) newKey() string {
	d.next++
	return fmt.Sprintf("%s:%s:%d", d.opts.KeyPrefix, cutSlug(d.opts.KeyPrefix, d.opts.Slug), d.next)
}

func (d *Draft) find(key string) int {
	for i, b := range d.blocks {
		if b.Key != "" && b.Key == key {
			return i
		}
	}
	return -1
}

func sortedSet(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	out := append([]string(nil), list...)
	sort.Strings(out)
	j := 0
	for i, v := range out {
		if i == 0 || v != out[j-1] {
			out[j] = v
			j++
		}
	}
	return out[:j]
}

func fingerprint(b *DraftBlock) string {
	raw, _ := json.Marshal(b.Content)
	blk := blockFromContent(b.Type, contentMap(NormalizeContent(b.Type, raw)))
	blk.normalize()
	data, _ := json.Marshal(struct {
		Type      string         `json:"t"`
		Content   map[string]any `json:"c"`
		Audiences []string       `json:"a"`
		Units     []string       `json:"u"`
	}{b.Type, blk.Content(), sortedSet(b.Audiences), sortedSet(b.Units)})
	return string(data)
}

func (d *Draft) authored(b *DraftBlock) bool {
	if b.Locked {
		return false
	}
	if b.Origin == OriginNew {
		return true
	}
	return fingerprint(b) != d.seed[b.Key]
}

// Append validates blocks and inserts them after a key ("^" for the top, "" for the end); it returns the new keys.
func (d *Draft) Append(after string, rationale *Rationale, blocks []Block) ([]string, error) {
	if d.opts.Mode == DraftReach {
		return nil, errors.New("blocks cannot be added to this page; change its editable blocks with draft_replace")
	}
	if len(blocks) == 0 {
		return nil, errors.New("blocks is empty")
	}
	var problems []string
	for i := range blocks {
		if err := blocks[i].Validate(d.opts.Contract, d.opts.Audiences); err != nil {
			problems = append(problems, fmt.Sprintf("blocks[%d] (%s): %v", i, blocks[i].Type, err))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("nothing was added; fix these blocks and send the call again:\n- %s", strings.Join(problems, "\n- "))
	}
	pos := len(d.blocks)
	switch strings.TrimSpace(after) {
	case "":
	case AfterFirst:
		pos = 0
	default:
		i := d.find(strings.TrimSpace(after))
		if i < 0 {
			return nil, fmt.Errorf("after: the draft has no block %q; call draft_view for the keys", after)
		}
		pos = i + 1
	}
	added := make([]*DraftBlock, 0, len(blocks))
	keys := make([]string, 0, len(blocks))
	for _, b := range blocks {
		r := b.Rationale
		if r == nil && rationale != nil {
			cp := *rationale
			r = &cp
		}
		db := &DraftBlock{Key: d.newKey(), Type: b.Type, Content: b.Content(), Audiences: b.Audiences, Units: b.Units, Rationale: r, Origin: OriginNew}
		added = append(added, db)
		keys = append(keys, db.Key)
	}
	d.blocks = append(d.blocks[:pos], append(added, d.blocks[pos:]...)...)
	d.save()
	return keys, nil
}

// Replace validates a block and puts it in place of an editable block, keeping its key and position.
func (d *Draft) Replace(key string, b Block) error {
	i := d.find(strings.TrimSpace(key))
	if i < 0 {
		return fmt.Errorf("the draft has no block %q; call draft_view for the keys", key)
	}
	cur := d.blocks[i]
	if cur.Locked {
		return fmt.Errorf("block %s is locked: %s", key, cur.LockReason)
	}
	if err := b.Validate(d.opts.Contract, d.opts.Audiences); err != nil {
		return fmt.Errorf("block (%s): %w", b.Type, err)
	}
	cur.Type, cur.Content = b.Type, b.Content()
	if b.Audiences != nil {
		cur.Audiences = b.Audiences
	}
	if b.Units != nil {
		cur.Units = b.Units
	}
	if b.Rationale != nil {
		cur.Rationale = b.Rationale
	}
	d.save()
	return nil
}

// Delete removes editable blocks; nothing is removed when one key is unknown or locked.
func (d *Draft) Delete(keys []string) error {
	if d.opts.Mode == DraftReach {
		return errors.New("blocks cannot be removed from this page; change its editable blocks with draft_replace")
	}
	if len(keys) == 0 {
		return errors.New("keys is empty")
	}
	drop := map[string]bool{}
	var problems []string
	for _, k := range keys {
		k = strings.TrimSpace(k)
		i := d.find(k)
		switch {
		case i < 0:
			problems = append(problems, fmt.Sprintf("the draft has no block %q", k))
		case d.blocks[i].Locked:
			problems = append(problems, fmt.Sprintf("block %s is locked: %s", k, d.blocks[i].LockReason))
		default:
			drop[k] = true
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("nothing was deleted: %s", strings.Join(problems, "; "))
	}
	kept := d.blocks[:0]
	for _, b := range d.blocks {
		if b.Key != "" && drop[b.Key] {
			if b.Origin == OriginSeed {
				d.deleted = append(d.deleted, b.Key)
			}
			continue
		}
		kept = append(kept, b)
	}
	d.blocks = kept
	d.save()
	return nil
}

// AddHint records a hint for another repository.
func (d *Draft) AddHint(h HintDraft) {
	d.hints = append(d.hints, h)
	d.save()
}

func (d *Draft) submit(summary string, uncovered []Uncovered) {
	d.summary, d.uncovered, d.submitted = summary, uncovered, true
	d.save()
}

// Diff maps the draft to its page change: new and changed editable blocks, each after the block before it, and deleted seed keys.
func (d *Draft) Diff() DraftDiff {
	var diff DraftDiff
	prev := AfterFirst
	for _, b := range d.blocks {
		if d.authored(b) {
			diff.Upserts = append(diff.Upserts, DraftUpsert{Block: *b, After: prev})
		}
		if b.Key != "" {
			prev = b.Key
		}
	}
	diff.Remove = append(diff.Remove, d.deleted...)
	return diff
}

type draftFile struct {
	Mode      string        `json:"mode"`
	Slug      string        `json:"slug"`
	Blocks    []*DraftBlock `json:"blocks"`
	Deleted   []string      `json:"deleted,omitempty"`
	Hints     []HintDraft   `json:"hints,omitempty"`
	Summary   string        `json:"summary,omitempty"`
	Uncovered []Uncovered   `json:"uncovered,omitempty"`
	Submitted bool          `json:"submitted"`
}

func (d *Draft) save() {
	if d.opts.Path == "" {
		return
	}
	data, err := json.MarshalIndent(draftFile{
		Mode: d.opts.Mode, Slug: d.opts.Slug, Blocks: d.blocks, Deleted: d.deleted, Hints: d.hints,
		Summary: d.summary, Uncovered: d.uncovered, Submitted: d.submitted,
	}, "", "  ")
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(d.opts.Path), 0o755) != nil {
		return
	}
	_ = os.WriteFile(d.opts.Path, append(data, '\n'), 0o644)
}

// View renders the draft as one line per block followed by the check status.
func (d *Draft) View() string {
	var b strings.Builder
	locked := 0
	for _, blk := range d.blocks {
		if blk.Locked {
			locked++
		}
	}
	fmt.Fprintf(&b, "Draft (%s, %s): %d blocks, %d editable, %d locked\n", d.opts.Mode, d.opts.Slug, len(d.blocks), len(d.blocks)-locked, locked)
	if len(d.blocks) == 0 {
		b.WriteString("(empty)\n")
	}
	for i, blk := range d.blocks {
		key := blk.Key
		if key == "" {
			key = fmt.Sprintf("#%d", i+1)
		}
		fmt.Fprintf(&b, "[%s]", key)
		if blk.Locked {
			b.WriteString(" (locked)")
		}
		b.WriteString(" " + blk.Type)
		switch blk.Type {
		case BlockHeading:
			fmt.Fprintf(&b, " h%v", blk.Content["level"])
		case BlockCode:
			fmt.Fprintf(&b, " %v", blk.Content["language"])
		case BlockList, BlockCallout:
			fmt.Fprintf(&b, " %v", blk.Content["variant"])
		}
		if len(blk.Audiences) > 0 {
			fmt.Fprintf(&b, " {audiences: %s}", strings.Join(blk.Audiences, ", "))
		}
		if len(blk.Units) > 0 {
			fmt.Fprintf(&b, " {units: %s}", strings.Join(blk.Units, ", "))
		}
		text := blk.Text()
		if blk.Locked && !oneOf(blk.Type, d.opts.Contract.Types) {
			text = compactLog(text, 300)
		}
		if text != "" {
			b.WriteString(": " + text)
		}
		b.WriteByte('\n')
	}
	b.WriteString(checkStatus(d.Check(nil)))
	return b.String()
}
