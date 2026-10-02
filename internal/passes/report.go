package passes

import (
	"fmt"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/changeset"
)

// Counts tally what a pass did.
type Counts struct {
	Created   int `json:"created,omitempty"`
	Updated   int `json:"updated,omitempty"`
	Deleted   int `json:"deleted,omitempty"`
	Imported  int `json:"imported,omitempty"`
	Unchanged int `json:"unchanged,omitempty"`
	Held      int `json:"held,omitempty"`
	Competing int `json:"competing,omitempty"`
	Facts     int `json:"facts,omitempty"`
	Queued    int `json:"queued,omitempty"`
	Hints     int `json:"hints,omitempty"`
}

// Report is the outcome of a pass run: the platform report plus local detail for output.
type Report struct {
	api.PassReport
	Counts   Counts               `json:"counts"`
	Claims   []agent.ClaimFinding `json:"claims,omitempty"`
	Warnings []string             `json:"warnings,omitempty"`
	Failing  bool                 `json:"failing,omitempty"`
	Recorded *Recorded            `json:"recorded,omitempty"`
	Usage    agent.Usage          `json:"usage"`
	Errors   []string             `json:"errors,omitempty"`
}

// Truncated reports whether the change set behind the report hit a size cap.
func (r *Report) Truncated(cs *changeset.ChangeSet) bool {
	return cs != nil && cs.Truncated
}

func (r *Report) warn(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

func (r *Report) impact(page api.PageRef, action, reason string) {
	r.Impact = append(r.Impact, api.Impact{Page: page, Action: action, Reason: reason})
}

func (r *Report) applied(c *api.Change) {
	if c == nil || c.ID == "" {
		return
	}
	r.ChangeIDs = append(r.ChangeIDs, c.ID)
	if c.HeldReason != nil && *c.HeldReason != "" {
		r.Counts.Held++
	}
	if c.Competing {
		r.Counts.Competing++
	}
	for _, w := range c.Warnings {
		if w.BlockKey != "" {
			r.warn("%s on %s: %s", w.Code, w.BlockKey, firstOf(w.Message, c.Page.Slug))
		} else {
			r.warn("%s: %s", w.Code, firstOf(w.Message, c.Page.Slug))
		}
	}
}

// Line is the short human summary of the counts.
func (c Counts) Line() string {
	var parts []string
	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	add(c.Updated, "updated")
	add(c.Created, "new")
	add(c.Imported, "imported")
	add(c.Deleted, "deletion proposals")
	add(c.Facts, "facts")
	add(c.Queued, "queued for review")
	add(c.Held, "held")
	add(c.Competing, "competing")
	add(c.Hints, "hints")
	if len(parts) == 0 {
		if c.Unchanged > 0 {
			return "no changes"
		}
		return ""
	}
	return strings.Join(parts, ", ")
}
