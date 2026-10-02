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

// Template keys of the app catalog.
const (
	TemplateAPIReference      = "api-reference"
	TemplateDeveloperGuide    = "developer-guide"
	TemplateUserGuide         = "user-guide"
	TemplateCustomerChangelog = "customer-changelog"
	TemplateInternalChangelog = "internal-changelog"
	TemplateRunbook           = "runbook"
	TemplateNucleusFacts      = "nucleus-facts"
	TemplateVerbatimDocs      = "verbatim-docs"
)

// TemplateSpaceTypes maps each template to the space type its target suggests ("" for none).
var TemplateSpaceTypes = map[string][]string{
	TemplateAPIReference:      {"api-reference"},
	TemplateDeveloperGuide:    {"product-docs"},
	TemplateUserGuide:         {"product-docs"},
	TemplateCustomerChangelog: {"release-notes"},
	TemplateInternalChangelog: {"release-notes"},
	TemplateRunbook:           {"handbook"},
	TemplateVerbatimDocs:      {"knowledge-base", "handbook"},
}

// TemplateTriggers are the default triggers of each template.
var TemplateTriggers = map[string][]string{
	TemplateAPIReference:      {TriggerPush, TriggerPR},
	TemplateDeveloperGuide:    {TriggerPush, TriggerPR},
	TemplateUserGuide:         {TriggerPush, TriggerPR},
	TemplateCustomerChangelog: {TriggerPush, TriggerRelease},
	TemplateInternalChangelog: {TriggerRelease},
	TemplateRunbook:           {TriggerPush},
	TemplateNucleusFacts:      {TriggerPush},
	TemplateVerbatimDocs:      {TriggerPush},
}

// PassTriggers returns the pass's triggers, else its template's defaults.
func PassTriggers(p Pass) []string {
	if len(p.Triggers) > 0 {
		return p.Triggers
	}
	return TemplateTriggers[p.Template]
}
