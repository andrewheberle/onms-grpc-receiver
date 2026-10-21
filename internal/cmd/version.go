package cmd

import (
	"context"
	"fmt"

	"github.com/andrewheberle/simplecommand"
	"github.com/bep/simplecobra"
)

type versionCommand struct {
	*simplecommand.Command
}

var Version = "dev"

func (c *versionCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	fmt.Printf("%s version: %s\n", cd.Root.Command.Name(), Version)

	return nil
}
