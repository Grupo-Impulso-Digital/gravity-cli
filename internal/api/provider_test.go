package api_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

const noProviderKeyMessage = "No AI provider is connected for this organization. An organization admin can connect Origin or add a provider key in Gravity › Settings › AI."

func TestNoProviderKeyIsTyped(t *testing.T) {
	srv := respond(http.StatusPaymentRequired, `{"error":{"code":"no_provider_key","message":"No LLM provider key is configured for this organization. Add one in Settings → AI."}}`)
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").WhoAmI(context.Background())
	nk, ok := api.AsNoProviderKey(err)
	if !ok {
		t.Fatalf("expected *api.NoProviderKeyError, got %T: %v", err, err)
	}
	if err.Error() != noProviderKeyMessage {
		t.Errorf("message = %q, want %q", err.Error(), noProviderKeyMessage)
	}
	if nk.StatusCode != http.StatusPaymentRequired || nk.Code != api.CodeNoProviderKey || nk.IsAuth() {
		t.Fatalf("underlying *APIError: %+v", nk.APIError)
	}
	if !api.IsLicenseError(err) || !api.HaltsRun(err) || !api.HasCode(err, api.CodeNoProviderKey) {
		t.Fatalf("a refusal that halts the run: %v", err)
	}
	if api.IsBudgetError(err) || api.StopsRun(err) || errors.Is(err, api.ErrUnauthorized) {
		t.Fatalf("not a spend cap, not a lost run, not a rejected token: %v", err)
	}
	wrapped := fmt.Errorf("guides: %w", fmt.Errorf("model call: %w", err))
	if _, ok := api.AsNoProviderKey(wrapped); !ok || !api.IsLicenseError(wrapped) || !api.HaltsRun(wrapped) {
		t.Fatalf("the typed error survives wrapping: %v", wrapped)
	}
}

func TestNoProviderKeyOnTheModelGateway(t *testing.T) {
	srv := respond(http.StatusPaymentRequired, `{"error":{"code":"no_provider_key","message":"No key."}}`)
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").Messages(context.Background(), api.MessagesRequest{})
	if _, ok := api.AsNoProviderKey(err); !ok || !api.HaltsRun(err) {
		t.Fatalf("err = %T %v", err, err)
	}
}

func TestNoProviderKeyOnAnotherStatusIsNotTyped(t *testing.T) {
	srv := respond(http.StatusBadRequest, `{"error":{"code":"no_provider_key"}}`)
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").WhoAmI(context.Background())
	if _, ok := api.AsNoProviderKey(err); ok || api.IsLicenseError(err) {
		t.Fatalf("should not be typed: %v", err)
	}
}

func TestNewNoProviderKeyError(t *testing.T) {
	err := api.NewNoProviderKeyError()
	if err.Error() != noProviderKeyMessage || !api.IsLicenseError(err) || !api.HaltsRun(err) || !api.HasCode(err, api.CodeNoProviderKey) {
		t.Fatalf("err = %v", err)
	}
}

const llmConfigWithSpend = `{
	"provider":"anthropic","model":"claude-sonnet-4-5","tone":"neutral","hasKey":true,"availableProviders":["anthropic"],
	"contextInjection":false,"serverTools":[],
	"spend":{
		"monthStart":"2026-10-01T00:00:00.000Z","resetsAt":"2026-11-01T00:00:00.000Z","daysElapsed":24.5,"daysInMonth":31,
		"org":{"feature":"all","spentUsd":42.1,"projectedUsd":53.3,"maxUsdPerRun":5,"maxUsdPerMonth":200},
		"features":[
			{"feature":"ask","spentUsd":3.1,"projectedUsd":3.9,"maxUsdPerRun":null,"maxUsdPerMonth":20},
			{"feature":"doc-agent","spentUsd":0,"projectedUsd":0,"maxUsdPerRun":null,"maxUsdPerMonth":null},
			{"feature":"cli-gateway","spentUsd":30,"projectedUsd":38,"maxUsdPerRun":2.5,"maxUsdPerMonth":null}
		],
		"unpricedCalls":0
	}
}`

func TestLLMConfigCarriesTheSpendCaps(t *testing.T) {
	c, s := fixtureServer(t, 200, llmConfigWithSpend)
	cfg, err := c.LLMConfig(context.Background())
	if err != nil || cfg.Spend == nil {
		t.Fatalf("llm config %+v %v", cfg, err)
	}
	expectRequest(t, s, "GET", "/api/llm/v1/config")
	if want := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC); !cfg.Spend.ResetsAt.Equal(want) {
		t.Errorf("resetsAt = %v", cfg.Spend.ResetsAt)
	}
	caps := cfg.Spend.Caps()
	if len(caps) != 3 || caps[0].Feature != api.SpendOrg || caps[1].Feature != "ask" || caps[2].Feature != "cli-gateway" {
		t.Fatalf("caps = %+v", caps)
	}
	org := caps[0]
	if *org.MaxUSDPerRun != 5 || *org.MaxUSDPerMonth != 200 || *org.SpentUSD != 42.1 {
		t.Errorf("org = %+v", org)
	}
	if ask := caps[1]; ask.MaxUSDPerRun != nil || *ask.MaxUSDPerMonth != 20 || *ask.SpentUSD != 3.1 {
		t.Errorf("ask = %+v", ask)
	}
}

func TestLLMConfigWithoutSpend(t *testing.T) {
	c, _ := fixtureServer(t, 200, `{"provider":"anthropic","model":"m","hasKey":true,"availableProviders":["anthropic"]}`)
	cfg, err := c.LLMConfig(context.Background())
	if err != nil || cfg.Spend != nil {
		t.Fatalf("llm config %+v %v", cfg, err)
	}
}

func TestSpendWithoutAnyCap(t *testing.T) {
	c, _ := fixtureServer(t, 200, `{"hasKey":true,"spend":{"org":{"feature":"all","spentUsd":1.5,"maxUsdPerRun":null,"maxUsdPerMonth":null},"features":[{"feature":"ask","spentUsd":1.5}]}}`)
	cfg, err := c.LLMConfig(context.Background())
	if err != nil || cfg.Spend == nil || len(cfg.Spend.Caps()) != 0 || *cfg.Spend.Org.SpentUSD != 1.5 {
		t.Fatalf("llm config %+v %v", cfg, err)
	}
}
