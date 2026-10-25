package api

import "time"

// SpendOrg is the feature key of the organization-wide spend line.
const SpendOrg = "all"

// SpendLine is one cap of the organization's AI spend and what the month has spent against it; the server may leave any amount out.
type SpendLine struct {
	Feature        string   `json:"feature"`
	SpentUSD       *float64 `json:"spentUsd,omitempty"`
	MaxUSDPerRun   *float64 `json:"maxUsdPerRun,omitempty"`
	MaxUSDPerMonth *float64 `json:"maxUsdPerMonth,omitempty"`
}

// Capped reports whether the line sets a per-run or a per-month limit.
func (l SpendLine) Capped() bool {
	return l.MaxUSDPerRun != nil || l.MaxUSDPerMonth != nil
}

// Spend is the AI spend overview inside GET /api/llm/v1/config: the organization's line and one line per feature.
type Spend struct {
	MonthStart time.Time   `json:"monthStart,omitzero"`
	ResetsAt   time.Time   `json:"resetsAt,omitzero"`
	Org        SpendLine   `json:"org"`
	Features   []SpendLine `json:"features"`
}

// Caps returns the lines that set a limit: the organization's first, then the features' in the server's order.
func (s Spend) Caps() []SpendLine {
	var out []SpendLine
	if s.Org.Capped() {
		org := s.Org
		org.Feature = SpendOrg
		out = append(out, org)
	}
	for _, f := range s.Features {
		if f.Capped() {
			out = append(out, f)
		}
	}
	return out
}
