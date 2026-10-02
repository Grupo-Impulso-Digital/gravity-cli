package config

// Pass kinds.
const (
	KindGuides    = "guides"
	KindReference = "reference"
	KindVerbatim  = "verbatim"
	KindChangelog = "changelog"
	KindNucleus   = "nucleus"
	KindCheck     = "check"
	KindCapture   = "capture"
)

// Triggers a pass can run on.
const (
	TriggerPR       = "pr"
	TriggerPush     = "push"
	TriggerRelease  = "release"
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
)

// TemplateKinds maps each pass template of the app catalog to the kind it produces.
var TemplateKinds = map[string]string{
	"api-reference":      KindReference,
	"developer-guide":    KindGuides,
	"user-guide":         KindGuides,
	"customer-changelog": KindChangelog,
	"internal-changelog": KindChangelog,
	"runbook":            KindGuides,
	"nucleus-facts":      KindNucleus,
	"verbatim-docs":      KindVerbatim,
}

var targetRequired = map[string]bool{
	KindGuides:    true,
	KindReference: true,
	KindVerbatim:  true,
	KindChangelog: true,
	KindCapture:   true,
}

// NeedsSpaceTarget reports whether passes of kind must target <site>/<space>.
func NeedsSpaceTarget(kind string) bool {
	return targetRequired[kind]
}
