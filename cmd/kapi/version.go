package main

import (
	"flag"
	"fmt"
	"io"
	"runtime/debug"

	"github.com/xaaha/cli"
)

var version = "dev"

func versionCmd(out io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "version",
		Short: "Print the kapi version",
		Flags: flag.NewFlagSet("version", flag.ContinueOnError),
		Run: func([]string) error {
			_, err := fmt.Fprintln(out, resolveVersion(version, debug.ReadBuildInfo))
			return err
		},
	}
}

func resolveVersion(ldflags string, buildInfo func() (*debug.BuildInfo, bool)) string {
	if ldflags != "dev" {
		return ldflags
	}
	if info, ok := buildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return ldflags
}
