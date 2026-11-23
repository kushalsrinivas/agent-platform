// Command platformctl is the operator CLI for a local or remote Agent Platform deployment.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/agent-platform/agent-platform/sdk/go/client"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "version":
		fmt.Println("platformctl", moduleVersion())
	case "health":
		if err := health(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func health(args []string) error {
	fs := flag.NewFlagSet("health", flag.ExitOnError)
	apiURL := fs.String("api", "http://localhost:8080", "API base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	api := client.New(*apiURL)

	meta, err := api.GetMeta(ctx)
	if err != nil {
		return err
	}
	if err := api.Ready(ctx); err != nil {
		fmt.Printf("api %s: NOT READY: %v\n", *apiURL, err)
		return err
	}

	fmt.Printf("api %s: ready\n", *apiURL)
	fmt.Printf("  version     %s\n", meta.Version)
	fmt.Printf("  commit      %s\n", meta.Commit)
	fmt.Printf("  environment %s\n", meta.Environment)
	return nil
}

func moduleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}

func usage() {
	fmt.Fprint(os.Stderr, `platformctl - Agent Platform operator CLI

Usage:
  platformctl version
  platformctl health [--api URL]

The CLI is a thin client: it only calls documented REST endpoints.
`)
}
