package main

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/4thel00z/qdrant-operator/internal/app"
	"github.com/4thel00z/qdrant-operator/internal/settings"
)

var version = "dev"

func main() {
	root, err := rootCommand()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := fang.Execute(context.Background(), root, fang.WithVersion(version), fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM)); err != nil {
		os.Exit(1)
	}
}

func rootCommand() (*cobra.Command, error) {
	v := viper.New()
	command := &cobra.Command{
		Use:          "qdrant-operator",
		Short:        "Kubernetes operator for Qdrant clusters, backups, restores, collections, access keys and migrations",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(command *cobra.Command, _ []string) error {
			config, err := settings.Load(v)
			if err != nil {
				return err
			}
			return app.Run(command.Context(), config, command.ErrOrStderr())
		},
	}
	if err := settings.RegisterFlags(command.Flags(), v); err != nil {
		return nil, err
	}
	return command, nil
}
