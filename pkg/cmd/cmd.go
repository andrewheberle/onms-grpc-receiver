package cmd

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/andrewheberle/simplecommand"
	"github.com/bep/simplecobra"
)

type rootCommand struct {
	logger *slog.Logger

	silent bool
	debug  bool

	*simplecommand.Command
}

func (c *rootCommand) Init(cd *simplecobra.Commandeer) error {
	if err := c.Command.Init(cd); err != nil {
		return err
	}

	cmd := cd.CobraCommand
	cmd.PersistentFlags().BoolVar(&c.debug, "debug", false, "Enable debug logging")
	cmd.PersistentFlags().BoolVar(&c.silent, "silent", false, "Disable all logging")

	return nil
}

func (c *rootCommand) PreRun(this, runner *simplecobra.Commandeer) error {
	if err := c.Command.PreRun(this, runner); err != nil {
		return err
	}

	// set up logger
	logLevel := new(slog.LevelVar)
	if c.silent {
		c.logger = slog.New(slog.DiscardHandler)
	} else {
		c.logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	}

	// switch on debug
	if c.debug {
		logLevel.Set(slog.LevelDebug)
	}

	c.logger.Debug("completed PreRun", "command", this.CobraCommand.Name())

	return nil
}

func (c *rootCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	return cd.CobraCommand.Help()
}

func Execute(args []string) error {
	rootCmd := &rootCommand{
		Command: simplecommand.New(
			"onms-grpc-receiver",
			"A gRPC receiver for OpenNMS",
			simplecommand.WithViper("onms_grpc", strings.NewReplacer("-", "_")),
		),
	}
	rootCmd.Command.SubCommands = []simplecobra.Commander{
		&spogCommand{
			logger: rootCmd.logger,
			Command: simplecommand.New(
				"spog",
				"Run in SPoG mode",
				simplecommand.WithViper("onms_grpc", strings.NewReplacer("-", "_")),
			),
		},
	}

	x, err := simplecobra.New(rootCmd)
	if err != nil {
		return err
	}

	if _, err := x.Execute(context.Background(), args); err != nil {
		return err
	}

	return nil
}
