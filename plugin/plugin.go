// Package plugin embeds the Gravity agent skill shipped with the CLI.
package plugin

import "embed"

// Skill holds skills/gravity: SKILL.md and its references, the single source for `gravity agent install`.
//
//go:embed skills
var Skill embed.FS
