// commerce-acceptance is the documented local entry point for #331 scenarios.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/acceptance"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "list":
		return listCommand(args[1:])
	case "run":
		return runCommand(args[1:])
	default:
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `commerce-acceptance — executable #331 scenario suite (#337)

Usage:
  go run ./cmd/commerce-acceptance list
  go run ./cmd/commerce-acceptance run [all|C01..C16] [flags]

Flags for run:
  --ready-only     run only scenarios marked ready (default for single scenario)
  --all            include blocked scenarios and extensions; nonzero when any blocked/failed
  --with-browser   also require browser evidence via make check-layout

Makefile shortcut:
  make commerce-acceptance
  make commerce-acceptance C05
  make commerce-acceptance-list
`)
}

func listCommand(args []string) int {
	if len(args) != 0 {
		usage()
		return 2
	}
	manifest, err := acceptance.LoadManifest()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load manifest: %v\n", err)
		return 1
	}
	if err := acceptance.Validate(manifest); err != nil {
		fmt.Fprintf(os.Stderr, "validate manifest: %v\n", err)
		return 1
	}
	for i := range manifest.Scenarios {
		scenario := &manifest.Scenarios[i]
		fmt.Println(scenario.SummaryLine())
		for j := range scenario.Assertions {
			assertion := &scenario.Assertions[j]
			status := assertion.Status
			if status == "" {
				status = acceptance.StatusReady
			}
			switch assertion.Kind {
			case acceptance.AssertionGoTest:
				fmt.Printf("  go_test %s/%s status=%s evidence=%s\n",
					assertion.Package, assertion.Run, status, assertion.Evidence)
			case acceptance.AssertionBrowser:
				fmt.Printf("  browser %s pages=%s evidence=%s\n",
					assertion.Runner, strings.Join(assertion.Pages, ","), assertion.Evidence)
			case acceptance.AssertionGate:
				fmt.Printf("  gate status=%s evidence=%s\n", status, assertion.Evidence)
			}
		}
		for _, extension := range scenario.Extensions {
			fmt.Printf("  extension blocked issues=%v %s\n", extension.Issues, extension.Title)
		}
	}
	return 0
}

func splitRunArgs(args []string) ([]string, []string) {
	var flags, targets []string
	literal := false
	for _, arg := range args {
		if arg == "--" && !literal {
			literal = true
			continue
		}
		if !literal && strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
		} else {
			targets = append(targets, arg)
		}
	}
	return flags, targets
}

func parseRunArgs(args []string) (acceptance.RunOptions, bool, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	readyOnly := fs.Bool("ready-only", false, "run only ready scenarios")
	allMode := fs.Bool("all", false, "treat blocked scenarios and extensions as failures")
	withBrowser := fs.Bool("with-browser", false, "also run browser evidence via make check-layout")
	flags, targets := splitRunArgs(args)
	if len(targets) > 1 {
		return acceptance.RunOptions{}, false, fmt.Errorf("run accepts one scenario, got %q", targets)
	}
	if err := fs.Parse(flags); err != nil {
		return acceptance.RunOptions{}, false, err
	}
	if *allMode && *readyOnly {
		return acceptance.RunOptions{}, false, errors.New("--all and --ready-only contradict each other")
	}
	target := "all"
	if len(targets) == 1 && !strings.EqualFold(targets[0], "all") {
		target = strings.ToUpper(targets[0])
	}
	explicit := target != "all"
	if explicit && !*allMode {
		*readyOnly = true
	}
	return acceptance.RunOptions{
		ReadyOnly: *readyOnly, WithBrowser: *withBrowser,
		ScenarioID: target, ExplicitSelection: explicit,
	}, *allMode, nil
}

func runCommand(args []string) int {
	opts, allMode, err := parseRunArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		return 2
	}
	manifest, err := acceptance.LoadManifest()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load manifest: %v\n", err)
		return 1
	}
	start := time.Now()
	results, err := acceptance.RunManifest(context.Background(), manifest, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		return 1
	}
	fmt.Print(acceptance.FormatResults(results))
	fmt.Printf("elapsed %s\n", time.Since(start).Round(time.Millisecond))
	return acceptance.ExitCode(results, allMode || opts.ScenarioID == "all")
}
