package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	worldclass "github.com/tatulea/worldclass-scheduler/internal"
)

const defaultConfigPath = "config.yaml"

func main() {
	var (
		cfgPath      string
		fetchShowAll bool
		scheduleLoop bool
	)

	rootCmd := &cobra.Command{
		Use:   "worldclass-scheduler",
		Short: "Automate fetching and booking of WorldClass classes",
	}
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.PersistentFlags().StringVar(&cfgPath, "config", envOrDefault("WORLDCLASS_CONFIG", defaultConfigPath), "path to configuration file")

	fetchCmd := &cobra.Command{
		Use:   "fetch",
		Short: "Fetch classes and print their status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := worldclass.LoadConfig(cfgPath)
			if err != nil {
				return err
			}
			return worldclass.RunFetch(cfg, worldclass.FetchOptions{ShowAll: fetchShowAll})
		},
	}
	fetchCmd.Flags().BoolVar(&fetchShowAll, "all", false, "show all classes, ignoring configured interests")

	scheduleCmd := &cobra.Command{
		Use:   "schedule",
		Short: "Attempt to book interested classes",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := worldclass.LoadConfig(cfgPath)
			if err != nil {
				return err
			}
			return worldclass.RunSchedule(cfg, worldclass.ScheduleOptions{Loop: scheduleLoop})
		},
	}
	scheduleCmd.Flags().BoolVar(&scheduleLoop, "loop", false, "continuously monitor and book upcoming classes")

	rootCmd.AddCommand(fetchCmd, scheduleCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
