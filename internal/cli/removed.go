package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func removedPointer(old, replacement string) error {
	return &ExitError{
		Code:    CodeError,
		Err:     fmt.Errorf("`gravity %s` was removed in gravity 1.0; use `%s`", old, replacement),
		ErrCode: "command_removed",
	}
}

var removed = []struct {
	name        string
	replacement string
	sub         map[string]string
}{
	{"auth", "gravity login", map[string]string{"login": "gravity login", "logout": "gravity logout", "status": "gravity status"}},
	{"doctor", "gravity status", nil},
	{"ping", "gravity status", nil},
	{"repos", "gravity status", nil},
	{"spaces", "gravity status", nil},
	{"sync", "gravity run", nil},
	{"docs", "gravity run", map[string]string{"generate": "gravity run", "init": "gravity run"}},
	{"release-notes", "gravity run", nil},
	{"coverage", "gravity check", nil},
	{"capture", "gravity run", nil},
	{"nucleus", "gravity run", map[string]string{"sync": "gravity run", "query": "gravity run"}},
}

func removedCommands() []*cobra.Command {
	cmds := make([]*cobra.Command, 0, len(removed))
	for _, r := range removed {
		cmds = append(cmds, &cobra.Command{
			Use:                r.name,
			Hidden:             true,
			DisableFlagParsing: true,
			RunE: func(_ *cobra.Command, args []string) error {
				old, repl := r.name, r.replacement
				for _, arg := range args {
					if to, ok := r.sub[arg]; ok {
						old, repl = r.name+" "+arg, to
						break
					}
				}
				return removedPointer(old, repl)
			},
		})
	}
	return cmds
}
