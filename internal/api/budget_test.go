package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

const budgetAction = "Ask an organization admin to raise the cap in Gravity › AI usage."

func f64(v float64) *float64 { return &v }

func TestBudgetExceededIsTyped(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		scope   string
		feature string
		limit   *float64
		spent   *float64
		resets  time.Time
		message string
	}{
		{
			name:    "repository run budget as the server sends it today",
			body:    `{"error":{"code":"budget_exceeded","message":"This run reached its cost budget.","details":[{"path":"budget.maxCostUsdPerRun","message":"5"}]}}`,
			scope:   api.BudgetScopeRun,
			limit:   f64(5),
			message: "This run's AI budget is used up: the limit is $5.00. " + budgetAction,
		},
		{
			name:    "repository monthly budget as the server sends it today",
			body:    `{"error":{"code":"budget_exceeded","message":"This repository reached its monthly budget.","details":[{"path":"budget.maxCostUsdPerMonth","message":"12.5"}]}}`,
			scope:   api.BudgetScopeMonth,
			limit:   f64(12.5),
			message: "The monthly AI budget is used up: the limit is $12.50. " + budgetAction,
		},
		{
			name:    "organization path in the list shape",
			body:    `{"error":{"code":"budget_exceeded","details":[{"path":"org.maxCostUsdPerMonth","message":"200"}]}}`,
			scope:   api.BudgetScopeMonth,
			limit:   f64(200),
			message: "The monthly AI budget is used up: the limit is $200.00. " + budgetAction,
		},
		{
			name:    "month with every field",
			body:    `{"error":{"code":"budget_exceeded","message":"Cap reached.","details":{"scope":"month","limitUsd":50,"spentUsd":52.104,"resetsAt":"2026-11-01T00:00:00Z"}}}`,
			scope:   api.BudgetScopeMonth,
			limit:   f64(50),
			spent:   f64(52.104),
			resets:  time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
			message: "The monthly AI budget is used up: $52.10 spent of a $50.00 limit; it resets on 2026-11-01. " + budgetAction,
		},
		{
			name:    "run with amounts",
			body:    `{"error":{"code":"budget_exceeded","details":{"scope":"run","limitUsd":5,"spentUsd":5.4}}}`,
			scope:   api.BudgetScopeRun,
			limit:   f64(5),
			spent:   f64(5.4),
			message: "This run's AI budget is used up: $5.40 spent of a $5.00 limit. " + budgetAction,
		},
		{
			name:    "feature with a name and a reset time",
			body:    `{"error":{"code":"budget_exceeded","details":{"scope":"feature","feature":"ask","limit":20,"spent":"20.01","resetsAt":"2026-11-01T13:30:00+02:00"}}}`,
			scope:   api.BudgetScopeFeature,
			feature: "ask",
			limit:   f64(20),
			spent:   f64(20.01),
			resets:  time.Date(2026, 11, 1, 11, 30, 0, 0, time.UTC),
			message: "The AI budget for the ask feature is used up: $20.01 spent of a $20.00 limit; it resets on 2026-11-01 11:30 UTC. " + budgetAction,
		},
		{
			name:    "feature without a name",
			body:    `{"error":{"code":"budget_exceeded","details":{"scope":"feature"}}}`,
			scope:   api.BudgetScopeFeature,
			message: "The AI budget for this feature is used up. " + budgetAction,
		},
		{
			name:    "a feature name implies the feature scope",
			body:    `{"error":{"code":"budget_exceeded","details":{"feature":"pipeline","spentUsd":3,"resetsAt":"2026-12-01"}}}`,
			scope:   api.BudgetScopeFeature,
			feature: "pipeline",
			spent:   f64(3),
			resets:  time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
			message: "The AI budget for the pipeline feature is used up: $3.00 spent; it resets on 2026-12-01. " + budgetAction,
		},
		{
			name:    "no details",
			body:    `{"error":{"code":"budget_exceeded","message":"This run reached its cost budget."}}`,
			message: "The AI budget is used up. " + budgetAction,
		},
		{
			name:    "an unknown scope is kept and the amounts still show",
			body:    `{"error":{"code":"budget_exceeded","details":{"scope":"Team","limitUsd":0.004,"spentUsd":0}}}`,
			scope:   "team",
			limit:   f64(0.004),
			spent:   f64(0),
			message: "The AI budget is used up: $0.00 spent of a $0.004 limit. " + budgetAction,
		},
		{
			name:    "unusable values are dropped",
			body:    `{"error":{"code":"budget_exceeded","details":{"scope":"run","limitUsd":-1,"spentUsd":"NaN","resetsAt":"soon"}}}`,
			scope:   api.BudgetScopeRun,
			message: "This run's AI budget is used up. " + budgetAction,
		},
		{
			name:    "details of another shape",
			body:    `{"error":{"code":"budget_exceeded","details":"cap"}}`,
			message: "The AI budget is used up. " + budgetAction,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := respond(http.StatusPaymentRequired, tc.body)
			defer srv.Close()

			_, err := api.New(srv.URL, "tok").WhoAmI(context.Background())
			var be *api.BudgetExceededError
			if !errors.As(err, &be) {
				t.Fatalf("expected *api.BudgetExceededError, got %T: %v", err, err)
			}
			if be.Scope != tc.scope || be.Feature != tc.feature || !be.ResetsAt.Equal(tc.resets) {
				t.Errorf("details = %+v", be.BudgetDetails)
			}
			if !samePtr(be.LimitUSD, tc.limit) || !samePtr(be.SpentUSD, tc.spent) {
				t.Errorf("amounts = limit %v spent %v", deref(be.LimitUSD), deref(be.SpentUSD))
			}
			if err.Error() != tc.message {
				t.Errorf("message = %q\nwant      %q", err.Error(), tc.message)
			}
			if be.StatusCode != http.StatusPaymentRequired || be.Code != api.CodeBudgetExceeded {
				t.Errorf("underlying *APIError: %+v", be.APIError)
			}
		})
	}
}

func samePtr(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func deref(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func TestBudgetExceededThroughTheChain(t *testing.T) {
	srv := respond(http.StatusPaymentRequired, `{"error":{"code":"budget_exceeded","details":{"scope":"month","limitUsd":50,"spentUsd":51}}}`)
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").WhoAmI(context.Background())
	wrapped := fmt.Errorf("guides: page refunds: %w", fmt.Errorf("model call: %w", err))

	be, ok := api.AsBudgetExceeded(wrapped)
	if !ok || be.Scope != api.BudgetScopeMonth {
		t.Fatalf("AsBudgetExceeded = %+v, %v", be, ok)
	}
	var ae *api.APIError
	if !api.IsBudgetError(wrapped) || !api.HaltsRun(wrapped) || !api.HasCode(wrapped, api.CodeBudgetExceeded) || !errors.As(wrapped, &ae) || ae.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("the typed error keeps the raw one reachable: %v", wrapped)
	}
	if api.StopsRun(wrapped) || api.IsLicenseError(wrapped) || ae.IsAuth() {
		t.Fatalf("a budget refusal is neither a stop, a license refusal nor an auth failure: %v", wrapped)
	}
}

func TestBudgetExceededOnTheModelGateway(t *testing.T) {
	srv := respond(http.StatusPaymentRequired, `{"error":{"code":"budget_exceeded","details":{"scope":"run","limitUsd":5,"spentUsd":5.2}}}`)
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").Messages(context.Background(), api.MessagesRequest{})
	be, ok := api.AsBudgetExceeded(err)
	if !ok || be.Scope != api.BudgetScopeRun || be.SpentUSD == nil || *be.SpentUSD != 5.2 || !api.HaltsRun(err) {
		t.Fatalf("err = %T %v", err, err)
	}
}

func TestOtherPaymentRefusalsAreNotBudgetErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"no provider key", http.StatusPaymentRequired, `{"error":{"code":"no_provider_key","message":"No key."}}`},
		{"budget code on another status", http.StatusBadRequest, `{"error":{"code":"budget_exceeded"}}`},
		{"non-json 402", http.StatusPaymentRequired, `payment required`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := respond(tc.status, tc.body)
			defer srv.Close()

			_, err := api.New(srv.URL, "tok").WhoAmI(context.Background())
			if api.IsBudgetError(err) {
				t.Fatalf("should not be a budget error: %v", err)
			}
			var ae *api.APIError
			if !errors.As(err, &ae) {
				t.Fatalf("expected an *api.APIError, got %T: %v", err, err)
			}
		})
	}
}

func TestBudgetDetailsMarshal(t *testing.T) {
	full := api.BudgetDetails{Scope: api.BudgetScopeMonth, Feature: "ask", LimitUSD: f64(50), SpentUSD: f64(52.5), ResetsAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	data, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"scope":"month","feature":"ask","limitUsd":50,"spentUsd":52.5,"resetsAt":"2026-11-01T00:00:00Z"}`; string(data) != want {
		t.Errorf("json = %s, want %s", data, want)
	}
	if data, _ = json.Marshal(api.BudgetDetails{}); string(data) != `{}` {
		t.Errorf("empty json = %s", data)
	}
}
