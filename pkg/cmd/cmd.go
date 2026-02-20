package cmd

import (
	"context"
	"strings"

	"github.com/andrewheberle/simplecommand"
	"github.com/andrewheberle/simplecommand/vipercommand"
	"github.com/bep/simplecobra"
)

type rootCommand struct {
	*simplecommand.Command
}

func (c *rootCommand) Init(cd *simplecobra.Commandeer) error {
	if err := c.Command.Init(cd); err != nil {
		return err
	}

	return nil
}

func (c *rootCommand) PreRun(this, runner *simplecobra.Commandeer) error {
	if err := c.Command.PreRun(this, runner); err != nil {
		return err
	}

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
		),
	}

	spog := &spogCommand{
		Command: vipercommand.New(
			"spog",
			"Run in SPoG mode",
			simplecommand.Long(`Run in Service Provider over gRPC (SPoG) mode. In this mode gRPC messages from any number of downstream
OpenNMS Horizon instances may be handled as all Heartbeat and AlarmUpdate messages include details of the downstream Horizon instance. Inventory and Event updates are not handled in this mode, only HeartBeat and Alarm updates.`),
		),
	}
	spog.EnvKeyReplacer = strings.NewReplacer("-", "_", ".", "_")
	spog.EnvPrefix = "onms_grpc"

	rootCmd.Command.SubCommands = []simplecobra.Commander{
		spog,
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
