package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/xaaha/cli"

	"github.com/xaaha/kapi/internal/render"
)

const pageRules = `rules:
  A page is an HTML fragment: prose tags (h1-h3, p, ul, ol, li, a, code, em, strong, table, blockquote, pre)
  plus the presets above. Top-level elements carry data-kapi-id; kapi adds the ones you leave out.
  <script>, <style>, style= and on* attributes and javascript: links are removed when the page renders.
  A misspelled or misplaced kapi-* element renders as a red error box naming the problem.
`

func newPresetCmd(out io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "preset",
		Short: "List the page presets, or print one preset's example and rules",
		Long:  "With no name, list every preset element a page can use. With a name, print its rules and an example to copy.",
		Flags: flag.NewFlagSet("preset", flag.ContinueOnError),
		Args:  []cli.ArgDef{{Name: "name", Desc: "Preset name, with or without the kapi- prefix"}},
		Examples: []cli.Example{
			{Command: "kapi preset", Description: "List every preset"},
			{Command: "kapi preset diagram", Description: "Show how to write a kapi-diagram"},
		},
		Run: func(args []string) error {
			if len(args) == 0 {
				return listPresets(out)
			}
			return showPreset(out, args[0])
		},
	}
}

func listPresets(out io.Writer) error {
	presets := render.Presets()
	width := 0
	for _, p := range presets {
		width = max(width, len(p.Name))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "presets[%d]:\n", len(presets))
	for _, p := range presets {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, p.Name, p.Summary)
	}
	b.WriteString(pageRules)
	b.WriteString("next:\n  kapi preset <name>  print a preset's rules and an example to copy\n")
	_, err := io.WriteString(out, b.String())
	return err
}

func showPreset(out io.Writer, name string) error {
	want := "kapi-" + strings.TrimPrefix(name, "kapi-")
	var names []string
	for _, p := range render.Presets() {
		if p.Name == want {
			_, err := fmt.Fprintf(out, "%s: %s\n\n%s\n", p.Name, p.Summary, p.Example)
			return err
		}
		names = append(names, strings.TrimPrefix(p.Name, "kapi-"))
	}
	return fmt.Errorf("no preset %q; choose one of: %s", name, strings.Join(names, ", "))
}
