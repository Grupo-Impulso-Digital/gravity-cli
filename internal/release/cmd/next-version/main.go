// Command next-version prints the newest release and the next version from the tags on stdin.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/release"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("next-version", flag.ContinueOnError)
	fs.SetOutput(errOut)
	bump := fs.String("bump", "patch", "part of the newest vX.Y.Z to bump: patch, minor or major")
	candidate := fs.Bool("rc", false, "print the next release candidate vX.Y.Z-rc.N of that version")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var tags []string
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			tags = append(tags, t)
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(errOut, "next-version: %v\n", err)
		return 2
	}
	next, err := release.Next(tags, *bump, *candidate)
	if err != nil {
		fmt.Fprintf(errOut, "next-version: %v\n", err)
		return 2
	}
	fmt.Fprintf(out, "latest=%s\nversion=%s\n", release.Latest(tags), next)
	return 0
}
