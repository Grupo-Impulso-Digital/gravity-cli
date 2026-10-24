package api

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Scopes of a budget_exceeded refusal (details.scope).
const (
	BudgetScopeRun     = "run"
	BudgetScopeMonth   = "month"
	BudgetScopeFeature = "feature"
)

const msgBudgetAction = "Ask an organization admin to raise the cap in Gravity › AI usage."

// BudgetDetails says which AI spend cap a budget_exceeded refusal hit; the server may leave any field out.
type BudgetDetails struct {
	Scope    string    `json:"scope,omitempty"`
	Feature  string    `json:"feature,omitempty"`
	LimitUSD *float64  `json:"limitUsd,omitempty"`
	SpentUSD *float64  `json:"spentUsd,omitempty"`
	ResetsAt time.Time `json:"resetsAt,omitzero"`
}

// BudgetExceededError is a refusal because an AI spend cap (this run, this month or one feature) is used up.
type BudgetExceededError struct {
	BudgetDetails
	*APIError
}

func (e *BudgetExceededError) Error() string {
	var b strings.Builder
	b.WriteString(e.subject())
	b.WriteString(" is used up")
	switch {
	case e.LimitUSD != nil && e.SpentUSD != nil:
		fmt.Fprintf(&b, ": %s spent of a %s limit", usd(*e.SpentUSD), usd(*e.LimitUSD))
	case e.LimitUSD != nil:
		fmt.Fprintf(&b, ": the limit is %s", usd(*e.LimitUSD))
	case e.SpentUSD != nil:
		fmt.Fprintf(&b, ": %s spent", usd(*e.SpentUSD))
	}
	if !e.ResetsAt.IsZero() {
		b.WriteString("; it resets on " + resetLabel(e.ResetsAt))
	}
	b.WriteString(". " + msgBudgetAction)
	return b.String()
}

// Unwrap exposes the raw *APIError.
func (e *BudgetExceededError) Unwrap() error { return e.APIError }

func (e *BudgetExceededError) subject() string {
	switch e.Scope {
	case BudgetScopeRun:
		return "This run's AI budget"
	case BudgetScopeMonth:
		return "The monthly AI budget"
	case BudgetScopeFeature:
		if e.Feature != "" {
			return "The AI budget for the " + e.Feature + " feature"
		}
		return "The AI budget for this feature"
	}
	return "The AI budget"
}

// AsBudgetExceeded returns the *BudgetExceededError in err's chain.
func AsBudgetExceeded(err error) (*BudgetExceededError, bool) {
	var be *BudgetExceededError
	if errors.As(err, &be) {
		return be, true
	}
	return nil, false
}

// IsBudgetError reports whether err is a spend cap refusal rather than an auth, license or input failure.
func IsBudgetError(err error) bool {
	_, ok := AsBudgetExceeded(err)
	return ok
}

func classifyBudget(e *APIError) *BudgetExceededError {
	if e.StatusCode != http.StatusPaymentRequired || e.Code != CodeBudgetExceeded {
		return nil
	}
	return &BudgetExceededError{BudgetDetails: budgetDetails(e), APIError: e}
}

func budgetDetails(e *APIError) BudgetDetails {
	d := BudgetDetails{
		Scope:    strings.ToLower(strings.TrimSpace(e.Field("scope"))),
		Feature:  strings.TrimSpace(e.Field("feature")),
		LimitUSD: e.amount("limitUsd", "limit"),
		SpentUSD: e.amount("spentUsd", "spent"),
		ResetsAt: parseReset(e.Field("resetsAt")),
	}
	for _, det := range e.Details {
		scope := budgetScopeOf(det.Path)
		if scope == "" {
			continue
		}
		if d.Scope == "" {
			d.Scope = scope
		}
		if d.LimitUSD == nil && d.Scope == scope {
			d.LimitUSD = parseAmount(det.Message)
		}
	}
	if d.Scope == "" && d.Feature != "" {
		d.Scope = BudgetScopeFeature
	}
	return d
}

func budgetScopeOf(path string) string {
	switch {
	case strings.HasSuffix(path, "maxCostUsdPerRun"):
		return BudgetScopeRun
	case strings.HasSuffix(path, "maxCostUsdPerMonth"):
		return BudgetScopeMonth
	}
	return ""
}

func (e *APIError) amount(keys ...string) *float64 {
	for _, key := range keys {
		if v := parseAmount(e.Field(key)); v != nil {
			return v
		}
	}
	return nil
}

func parseAmount(s string) *float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return nil
	}
	return &v
}

func parseReset(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func resetLabel(t time.Time) string {
	t = t.UTC()
	if t.Equal(t.Truncate(24 * time.Hour)) {
		return t.Format(time.DateOnly)
	}
	return t.Format("2006-01-02 15:04") + " UTC"
}

func usd(v float64) string {
	if v > 0 && v < 0.01 {
		return "$" + strconv.FormatFloat(v, 'f', -1, 64)
	}
	return fmt.Sprintf("$%.2f", v)
}
