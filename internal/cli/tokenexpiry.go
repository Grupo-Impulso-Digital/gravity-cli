package cli

import (
	"fmt"
	"math"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ci"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/report"
)

const (
	tokenWarnWindow = 14 * 24 * time.Hour
	dateLayout      = "2006-01-02"

	warnTokenExpired  = "token_expired"
	warnTokenExpiring = "token_expiring"
)

func tokenNoun(kind string) string {
	if kind == api.TokenKindRepo {
		return "repository token"
	}
	return "token"
}

func tokenExpiryNotice(kind string, expiresAt *string, now time.Time) (code, message string) {
	if expiresAt == nil || *expiresAt == "" {
		return "", ""
	}
	t, err := time.Parse(time.RFC3339, *expiresAt)
	if err != nil {
		return "", ""
	}
	noun, renew := tokenNoun(kind), api.RenewHint(kind)
	switch left := t.Sub(now); {
	case left <= 0:
		return warnTokenExpired, fmt.Sprintf("the %s expired on %s; %s", noun, t.Format(dateLayout), renew)
	case left < tokenWarnWindow:
		return warnTokenExpiring, fmt.Sprintf("the %s expires in %s (%s); %s", noun, plural(int(math.Ceil(left.Hours()/24)), "day", "days"), t.Format(dateLayout), renew)
	}
	return "", ""
}

func (a *app) warnTokenExpiry(c ci.Context, who *api.WhoAmI, fallbackKind string) {
	if who == nil || who.Token == nil {
		return
	}
	code, message := tokenExpiryNotice(firstNonEmpty(who.Token.Kind, fallbackKind), who.Token.ExpiresAt, a.now())
	if code == "" {
		return
	}
	if c.IsCI() && c.Provider == ci.GitHub {
		title := "Gravity token expiring"
		if code == warnTokenExpired {
			title = "Gravity token expired"
		}
		a.ui.WarnLine(code, message, report.GitHubWarning(title, message))
		return
	}
	a.ui.Warn(code, message)
}

func tokenExpiryLine(expiresAt *string) string {
	if expiresAt == nil || *expiresAt == "" {
		return "Expires: never"
	}
	t, err := time.Parse(time.RFC3339, *expiresAt)
	if err != nil {
		return "Expires: " + *expiresAt
	}
	return "Expires: " + t.Format(dateLayout) + " (run gravity ci setup again before then)"
}
