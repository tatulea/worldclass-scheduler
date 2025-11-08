package main

import (
	"fmt"
	"os"

	worldclass "github.com/tatulea/worldclass-scheduler/internal"
)

const defaultConfigPath = "config.yaml"

func main() {
	cfgPath := defaultConfigPath
	if env := os.Getenv("WORLDCLASS_CONFIG"); env != "" {
		cfgPath = env
	}

	cfg, err := worldclass.LoadConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	if len(os.Args) < 2 {
		fmt.Println("expected 'fetch' or 'schedule' subcommands")
		os.Exit(1)
	}

	var cmdErr error
	switch os.Args[1] {
	case "fetch":
		cmdErr = worldclass.RunFetch(cfg, os.Args[2:])
	case "schedule":
		cmdErr = worldclass.RunSchedule(cfg, os.Args[2:])
	default:
		fmt.Printf("unknown subcommand: %s\n", os.Args[1])
		os.Exit(1)
	}

	if cmdErr != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", os.Args[1], cmdErr)
		os.Exit(1)
	}
}
