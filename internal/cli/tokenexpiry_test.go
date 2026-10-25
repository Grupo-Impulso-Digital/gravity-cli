package cli

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func repoWhoamiExpiring(at string) string {
	return strings.Replace(whoamiRepo, `"expiresAt":null`, `"expiresAt":"`+at+`"`, 1)
}

func githubPush(t *testing.T, h *harness) {
	t.Helper()
	for k, v := range map[string]string{
		"CI": "true", "GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/main", "GITHUB_REF_NAME": "main",
		"GITHUB_SHA": gitCmd(t, h.dir, "rev-parse", "HEAD"),
	} {
		h.env[k] = v
	}
}

func TestTokenExpiryNoticeNamesTheKindAndHowToRenew(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	at := func(s string) *string { return &s }
	cases := []struct {
		name      string
		kind      string
		expiresAt *string
		code      string
		contains  []string
	}{
		{"repo expiring", api.TokenKindRepo, at("2026-10-05T12:00:00Z"), "token_expiring", []string{"the repository token expires in 4 days (2026-10-05)", "run `gravity ci setup`", "CLI & tokens › Repo tokens", "update the CI secret GRAVITY_REPO_TOKEN"}},
		{"repo expired", api.TokenKindRepo, at("2026-09-30T12:00:00Z"), "token_expired", []string{"the repository token expired on 2026-09-30", "run `gravity ci setup`"}},
		{"user expiring", api.TokenKindUser, at("2026-10-05T12:00:00Z"), "token_expiring", []string{"the token expires in 4 days (2026-10-05); run `gravity login` to renew it"}},
		{"user expired", api.TokenKindUser, at("2026-09-30T12:00:00Z"), "token_expired", []string{"the token expired on 2026-09-30; run `gravity login` to renew it"}},
		{"org expiring", api.TokenKindOrg, at("2026-10-02T11:00:00Z"), "token_expiring", []string{"the token expires in 1 day (2026-10-02)", "CLI & tokens"}},
		{"unknown kind", "", at("2026-10-05T12:00:00Z"), "token_expiring", []string{"run `gravity login`", "CLI & tokens"}},
		{"thirteen days left", api.TokenKindRepo, at("2026-10-14T11:00:00Z"), "token_expiring", []string{"13 days"}},
		{"fourteen days left", api.TokenKindRepo, at("2026-10-15T12:00:00Z"), "", nil},
		{"far away", api.TokenKindRepo, at("2027-10-01T12:00:00Z"), "", nil},
		{"never", api.TokenKindRepo, nil, "", nil},
		{"empty", api.TokenKindRepo, at(""), "", nil},
		{"unparseable", api.TokenKindRepo, at("soon"), "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, msg := tokenExpiryNotice(tc.kind, tc.expiresAt, now)
			if code != tc.code || (tc.code == "") != (msg == "") {
				t.Fatalf("notice = %q %q", code, msg)
			}
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("missing %q in %q", want, msg)
				}
			}
		})
	}
}

func TestStatusWarnsAboutAnExpiringRepositoryTokenInItsOwnWords(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_REPO_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, repoWhoamiExpiring("2026-10-05T12:00:00Z"))
	expectCode(t, h, h.run("status", "--json"), 0)
	var msg string
	for _, w := range h.envelope()["data"].(map[string]any)["capabilityWarnings"].([]any) {
		if w := w.(map[string]any); w["code"] == "token_expiring" {
			msg = w["message"].(string)
		}
	}
	for _, want := range []string{"the repository token expires in 4 days (2026-10-05)", "run `gravity ci setup`", "CLI & tokens › Repo tokens", "update the CI secret GRAVITY_REPO_TOKEN"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %q in %q", want, msg)
		}
	}
	if strings.Contains(msg, "gravity login") {
		t.Fatalf("a repository token is not renewed by logging in: %q", msg)
	}
}

func TestRunInGitHubActionsAnnotatesAnExpiringToken(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_REPO_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, repoWhoamiExpiring("2026-10-05T12:00:00Z"))
	h.commitSpec(t)
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	githubPush(t, h)
	expectCode(t, h, h.run("run"), 0)
	want := "::warning title=Gravity token expiring::the repository token expires in 4 days (2026-10-05); run `gravity ci setup` to mint a new one, or mint one under CLI & tokens > Repo tokens in the app, then update the CI secret GRAVITY_REPO_TOKEN"
	if !strings.Contains(h.stderr.String(), want) || strings.Contains(h.stderr.String(), "! the repository token") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	if n := len(h.platform.find("GET", "/api/v1/whoami")); n != 1 {
		t.Fatalf("the expiry comes from the whoami the run already makes, got %d requests", n)
	}
}

func TestRunWarnsPlainlyOutsideGitHubActions(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_REPO_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, repoWhoamiExpiring("2026-10-05T12:00:00Z"))
	h.commitSpec(t)
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	h.env["CI"] = "true"
	h.env["GITLAB_CI"] = "true"
	h.env["CI_PIPELINE_SOURCE"] = "push"
	h.env["CI_COMMIT_REF_NAME"] = "main"
	h.env["CI_COMMIT_SHA"] = gitCmd(t, h.dir, "rev-parse", "HEAD")
	expectCode(t, h, h.run("run"), 0)
	if !strings.Contains(h.stderr.String(), "! the repository token expires in 4 days (2026-10-05); run `gravity ci setup`") || strings.Contains(h.stderr.String(), "::warning") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestRunLocallyRecordsTheExpiryWarningInTheEnvelope(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.platform.json("GET /api/v1/whoami", 200, strings.Replace(whoamiUser, `"expiresAt":"2026-12-30T12:00:00Z"`, `"expiresAt":"2026-10-05T12:00:00Z"`, 1))
	h.commitSpec(t)
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	expectCode(t, h, h.run("run", "--json"), 0)
	var codes []string
	for _, w := range h.envelope()["warnings"].([]any) {
		codes = append(codes, w.(map[string]any)["code"].(string))
	}
	if !slices.Contains(codes, "token_expiring") {
		t.Fatalf("warnings = %v", codes)
	}
	if !strings.Contains(h.stderr.String(), "the token expires in 4 days (2026-10-05); run `gravity login` to renew it") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestRunStaysQuietWhileTheTokenHasTimeLeft(t *testing.T) {
	for name, whoami := range map[string]string{
		"never expires":       whoamiRepo,
		"a year left":         repoWhoamiExpiring("2027-09-30T12:00:00Z"),
		"fifteen days left":   repoWhoamiExpiring("2026-10-16T12:00:00Z"),
		"expiry not reported": strings.Replace(whoamiRepo, `,"expiresAt":null`, ``, 1),
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.env["GRAVITY_REPO_TOKEN"] = "gr_repo_ci"
			h.platform.json("GET /api/v1/whoami", 200, whoami)
			h.commitSpec(t)
			h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
			githubPush(t, h)
			expectCode(t, h, h.run("run"), 0)
			if strings.Contains(h.stderr.String(), "token expire") || strings.Contains(h.stdout.String(), "token expire") {
				t.Fatalf("stderr = %s", h.stderr.String())
			}
		})
	}
}

func TestCISetupSummaryShowsWhenTheMintedTokenExpires(t *testing.T) {
	mint := func(h *harness, expiresAt string) {
		h.platform.json("POST /api/v1/repos/cr_1/tokens", 201, `{"token":"gr_repo_secret","key":{"keyHint":"r2D2","scopes":["repo:connect","runs:write"],"expiresAt":`+expiresAt+`}}`)
	}
	t.Run("a dated token", func(t *testing.T) {
		h := newHarness(t)
		h.env["GRAVITY_TOKEN"] = "gr_user_abc"
		h.write(".gravity.yaml", "version: 2\n")
		mint(h, `"2027-10-01T12:00:00.000Z"`)
		expectCode(t, h, h.run("ci", "setup", "--provider", "github", "--no-secret"), 0)
		if !strings.Contains(h.stdout.String(), "Expires: 2027-10-01 (run gravity ci setup again before then)") {
			t.Fatalf("stdout = %s", h.stdout.String())
		}
		body := h.platform.find("POST", "/api/v1/repos/cr_1/tokens")[0].Body
		if v, ok := body["expiresInDays"]; !ok || v != nil {
			t.Fatalf("the server picks the default lifetime: %v", body)
		}
	})
	t.Run("a token without expiry", func(t *testing.T) {
		h := newHarness(t)
		h.env["GRAVITY_TOKEN"] = "gr_user_abc"
		h.write(".gravity.yaml", "version: 2\n")
		mint(h, `null`)
		expectCode(t, h, h.run("ci", "setup", "--provider", "github", "--no-secret"), 0)
		if !strings.Contains(h.stdout.String(), "Expires: never") {
			t.Fatalf("stdout = %s", h.stdout.String())
		}
	})
	t.Run("the expiry in the envelope", func(t *testing.T) {
		h := newHarness(t)
		h.env["GRAVITY_TOKEN"] = "gr_user_abc"
		h.write(".gravity.yaml", "version: 2\n")
		mint(h, `"2027-10-01T12:00:00.000Z"`)
		expectCode(t, h, h.run("ci", "setup", "--provider", "github", "--no-secret", "--json"), 0)
		token := h.envelope()["data"].(map[string]any)["token"].(map[string]any)
		if token["expiresAt"] != "2027-10-01T12:00:00.000Z" {
			t.Fatalf("token = %v", token)
		}
	})
	t.Run("no token minted", func(t *testing.T) {
		h := newHarness(t)
		h.env["GRAVITY_TOKEN"] = "gr_user_abc"
		h.write(".gravity.yaml", "version: 2\n")
		h.platform.json("POST /api/v1/repos/cr_1/tokens", 403, `{"error":{"code":"forbidden","message":"no"}}`)
		expectCode(t, h, h.run("ci", "setup", "--provider", "github", "--no-secret"), 0)
		if strings.Contains(h.stdout.String(), "Expires:") {
			t.Fatalf("stdout = %s", h.stdout.String())
		}
	})
}

func TestAnExpiredProfileRepositoryTokenGetsNoLoginAdvice(t *testing.T) {
	h := newHarness(t)
	writeProfiles(t, h.config, fmt.Sprintf("version: 2\ncurrent: acme\nprofiles:\n  acme:\n    apiUrl: %s\n    org: acme\n    token: gr_repo_old\n    tokenKind: repo\n", h.platform.srv.URL))
	h.platform.json("GET /api/v1/whoami", 401, `{"error":{"code":"token_expired","message":"Token expired"}}`)
	expectCode(t, h, h.run("whoami"), 4)
	if !strings.Contains(h.stderr.String(), "run `gravity ci setup`") || strings.Contains(h.stderr.String(), "gravity login") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}
