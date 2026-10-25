package api_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/version"
)

type recordedSleeps struct {
	d []time.Duration
}

func (r *recordedSleeps) sleep(_ context.Context, d time.Duration) error {
	r.d = append(r.d, d)
	return nil
}

func newTestClient(url string) (*api.Client, *recordedSleeps) {
	c := api.New(url, "tok")
	rec := &recordedSleeps{}
	c.Sleep = rec.sleep
	return c, rec
}

func TestHeadersAndUserAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		want := "gravity-cli/" + version.String() + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
		if got := r.Header.Get("User-Agent"); got != want {
			t.Errorf("User-Agent = %q, want %q", got, want)
		}
		if r.Method == http.MethodPost && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	c, _ := newTestClient(srv.URL)
	if _, err := c.WhoAmI(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRetriesServerErrorsWithBackoff(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"organizationId":"org_1"}`)
	}))
	defer srv.Close()
	c, rec := newTestClient(srv.URL)
	who, err := c.WhoAmI(context.Background())
	if err != nil || who.OrganizationID != "org_1" {
		t.Fatalf("WhoAmI = %+v, %v", who, err)
	}
	if calls.Load() != 3 || len(rec.d) != 2 || rec.d[0] != time.Second || rec.d[1] != 2*time.Second {
		t.Fatalf("calls=%d sleeps=%v", calls.Load(), rec.d)
	}
}

func TestRetryHonoursRetryAfterAndGivesUp(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":"rate_limited","message":"Slow down"}}`)
	}))
	defer srv.Close()
	c, rec := newTestClient(srv.URL)
	err := c.Post(context.Background(), "/api/v1/runs/r/heartbeat", map[string]any{}, nil)
	var ae *api.APIError
	if !errors.As(err, &ae) || ae.Code != api.CodeRateLimited || ae.RetryAfter != 7 {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 3 || len(rec.d) != 2 || rec.d[0] != 7*time.Second {
		t.Fatalf("calls=%d sleeps=%v", calls.Load(), rec.d)
	}
}

func TestConcurrencyLimitedIsRetriedAfterTheServerSaysSo(t *testing.T) {
	const body = `{"error":{"code":"concurrency_limited","message":"Too many AI calls are running for this organization.","retryAfter":5}}`
	cases := []struct {
		name   string
		header string
	}{
		{"header and body", "5"},
		{"body only", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) <= 2 {
					if tc.header != "" {
						w.Header().Set("Retry-After", tc.header)
					}
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = io.WriteString(w, body)
					return
				}
				_, _ = io.WriteString(w, `{"organizationId":"org_1"}`)
			}))
			defer srv.Close()
			c, rec := newTestClient(srv.URL)
			who, err := c.WhoAmI(context.Background())
			if err != nil || who.OrganizationID != "org_1" {
				t.Fatalf("WhoAmI = %+v, %v", who, err)
			}
			if calls.Load() != 3 || len(rec.d) != 2 || rec.d[0] != 5*time.Second || rec.d[1] != 5*time.Second {
				t.Fatalf("calls=%d sleeps=%v", calls.Load(), rec.d)
			}
		})
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	c, rec := newTestClient(srv.URL)
	err := c.Post(context.Background(), "/api/v1/runs/r/heartbeat", map[string]any{}, nil)
	var ae *api.APIError
	if !errors.As(err, &ae) || ae.Code != api.CodeConcurrencyLimited || ae.RetryAfter != 5 || api.HaltsRun(err) || api.IsLicenseError(err) {
		t.Fatalf("err = %v", err)
	}
	if len(rec.d) != 2 {
		t.Fatalf("sleeps = %v", rec.d)
	}
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 413} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(status)
		}))
		c, rec := newTestClient(srv.URL)
		_, err := c.WhoAmI(context.Background())
		srv.Close()
		if err == nil || calls.Load() != 1 || len(rec.d) != 0 {
			t.Fatalf("status %d: err=%v calls=%d", status, err, calls.Load())
		}
	}
}

func TestNotFoundIsAnError(t *testing.T) {
	srv := respond(http.StatusNotFound, `{"error":{"code":"repo_not_connected","message":"Repository github.com/acme/x is not connected."}}`)
	defer srv.Close()
	c, _ := newTestClient(srv.URL)
	_, err := c.Plan(context.Background(), api.PlanQuery{Repo: "github.com/acme/x", Trigger: "push"})
	if !errors.Is(err, api.ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "gravity setup") {
		t.Errorf("missing setup hint: %v", err)
	}
}

func TestErrorEnvelopeDecoding(t *testing.T) {
	srv := respond(http.StatusConflict, `{"error":{"code":"lease_held","message":"Another run holds main","retryAfter":30,"holder":{"runId":"prun_1","headSha":"abc","expiresAt":"2026-10-01T12:10:00Z"},"details":[{"path":"branch","message":"main"}]}}`)
	defer srv.Close()
	c, _ := newTestClient(srv.URL)
	_, err := c.StartRun(context.Background(), "", api.StartRunRequest{Trigger: "push"})
	var ae *api.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %T %v", err, err)
	}
	if ae.RetryAfter != 30 || ae.Holder == nil || ae.Holder.RunID != "prun_1" || len(ae.Details) != 1 {
		t.Fatalf("decoded %+v", ae)
	}
	if !errors.Is(err, api.ErrLeaseHeld) || api.StopsRun(err) {
		t.Fatal("lease_held classification")
	}
	if !strings.Contains(err.Error(), "branch: main") || !strings.Contains(err.Error(), "409 lease_held") {
		t.Errorf("message = %q", err.Error())
	}
}

func TestRunLivenessCodes(t *testing.T) {
	cases := []struct {
		code     string
		lost     bool
		finished bool
	}{
		{api.CodeLeaseLost, true, false},
		{api.CodeRunNotRunning, false, true},
		{api.CodeConflict, false, false},
	}
	for _, tc := range cases {
		srv := respond(http.StatusConflict, `{"error":{"code":"`+tc.code+`","message":"x"}}`)
		c, _ := newTestClient(srv.URL)
		_, err := c.Heartbeat(context.Background(), "prun_1")
		srv.Close()
		if errors.Is(err, api.ErrLeaseLost) != tc.lost || errors.Is(err, api.ErrRunNotRunning) != tc.finished {
			t.Errorf("%s: lost=%v finished=%v", tc.code, errors.Is(err, api.ErrLeaseLost), errors.Is(err, api.ErrRunNotRunning))
		}
		if api.StopsRun(err) != (tc.lost || tc.finished) {
			t.Errorf("%s: StopsRun = %v", tc.code, api.StopsRun(err))
		}
	}
}

func TestTokenExpiredHint(t *testing.T) {
	srv := respond(http.StatusUnauthorized, `{"error":{"code":"token_expired","message":"Token expired"}}`)
	defer srv.Close()
	c, _ := newTestClient(srv.URL)
	_, err := c.WhoAmI(context.Background())
	if !errors.Is(err, api.ErrUnauthorized) || !strings.Contains(err.Error(), "gravity login") {
		t.Fatalf("err = %v", err)
	}
}

func TestTokenExpiredHintFollowsTheTokenKind(t *testing.T) {
	const generic = `{"error":{"code":"token_expired","message":"Token expired"}}`
	cases := []struct {
		name    string
		token   string
		body    string
		kind    string
		want    []string
		wantNot []string
	}{
		{"repo", "gr_repo_x", generic, api.TokenKindRepo, []string{"run `gravity ci setup`", "CLI & tokens › Repo tokens", "update the CI secret GRAVITY_REPO_TOKEN"}, []string{"gravity login"}},
		{"user", "gr_user_x", generic, api.TokenKindUser, []string{"run `gravity login` to renew it"}, []string{"gravity ci setup"}},
		{"org", "sk_live_x", generic, api.TokenKindOrg, []string{"mint a new one under CLI & tokens in the app"}, []string{"gravity login", "gravity ci setup"}},
		{"unknown", "tok", generic, "", []string{"run `gravity login`", "CLI & tokens"}, []string{"gravity ci setup"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := respond(http.StatusUnauthorized, tc.body)
			defer srv.Close()
			_, err := api.New(srv.URL, tc.token).WhoAmI(context.Background())
			var ae *api.APIError
			if !errors.As(err, &ae) || ae.TokenKind != tc.kind || ae.Code != api.CodeTokenExpired {
				t.Fatalf("err = %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing %q in %v", want, err)
				}
			}
			for _, not := range tc.wantNot {
				if strings.Contains(err.Error(), not) {
					t.Errorf("unexpected %q in %v", not, err)
				}
			}
		})
	}
}

func TestTokenExpiredKeepsAServerMessageThatSaysWhatToDo(t *testing.T) {
	for _, msg := range []string{
		"This repository token has expired. Mint a new one with gravity ci setup, or under CLI & tokens in the app, and update the CI secret.",
		"This token has expired. Run gravity login.",
		"This organization key has expired. Mint a new one under CLI & tokens in the app.",
	} {
		srv := respond(http.StatusUnauthorized, `{"error":{"code":"token_expired","message":"`+msg+`"}}`)
		_, err := api.New(srv.URL, "gr_repo_x").WhoAmI(context.Background())
		srv.Close()
		if want := msg + " [401 token_expired]"; err == nil || err.Error() != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}

func TestTokenKindOf(t *testing.T) {
	for token, want := range map[string]string{"gr_user_a": api.TokenKindUser, "gr_repo_a": api.TokenKindRepo, "sk_live_a": api.TokenKindOrg, "tok": "", "": ""} {
		if got := api.TokenKindOf(token); got != want {
			t.Errorf("TokenKindOf(%q) = %q, want %q", token, got, want)
		}
	}
}

func TestHTMLErrorSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html><head><title>Bad gateway</title></head></html>")
	}))
	defer srv.Close()
	c, _ := newTestClient(srv.URL)
	_, err := c.WhoAmI(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Bad gateway") || strings.Contains(err.Error(), "<html") {
		t.Fatalf("err = %v", err)
	}
}

func TestNetworkErrorRetriesGetsAndModelCalls(t *testing.T) {
	c, rec := newTestClient("http://127.0.0.1:1")
	c.HTTPClient = &http.Client{Timeout: time.Second}
	if _, err := c.WhoAmI(context.Background()); err == nil || len(rec.d) != 2 {
		t.Fatalf("GET: err=%v sleeps=%v", err, rec.d)
	}
	rec.d = nil
	if err := c.Logout(context.Background()); err == nil || len(rec.d) != 0 {
		t.Fatalf("POST: err=%v sleeps=%v", err, rec.d)
	}
	rec.d = nil
	if _, err := c.Messages(context.Background(), api.MessagesRequest{}); err == nil || len(rec.d) <= 2 {
		t.Fatalf("messages: err=%v sleeps=%v", err, rec.d)
	}
	var waited time.Duration
	for _, d := range rec.d {
		waited += d
	}
	if waited >= api.DefaultLLMRetryCap {
		t.Fatalf("messages waited %s", waited)
	}
}

func TestNoBaseURL(t *testing.T) {
	if _, err := api.New("", "t").WhoAmI(context.Background()); err == nil {
		t.Fatal("want error")
	}
}

func TestStartRunRetriesReplayTheSameClientKeyAndBody(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(data))
		if len(bodies) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, `{"run":{"id":"prun_1"}}`)
	}))
	defer srv.Close()
	c, _ := newTestClient(srv.URL)
	if _, err := c.StartRun(context.Background(), "", api.StartRunRequest{ClientKey: "ck_1", Trigger: "push"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 || bodies[0] != bodies[1] || bodies[1] != bodies[2] || !strings.Contains(bodies[0], `"clientKey":"ck_1"`) {
		t.Fatalf("bodies = %q", bodies)
	}
}
