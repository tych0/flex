package main

import (
	"fmt"
	"strconv"

	"github.com/niemeyer/flex"
	"github.com/niemeyer/flex/internal/gnuflag"
)

type moveCmd struct {
	verbose bool
	stop    bool
}

const moveUsage = `
flex move [remote:]container [remote:]container

Live migrates a container
`

func (c *moveCmd) usage() string {
	return moveUsage
}

func (c *moveCmd) flags() {
	gnuflag.BoolVar(&c.verbose, "verbose", false, "emit verbose criu logs")
	gnuflag.BoolVar(&c.stop, "stop", true, "stop the container when a checkpoint is done")
}

func (c *moveCmd) run(args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	config, err := flex.LoadConfig()
	if err != nil {
		return err
	}

	sourced, name, err := flex.NewClient(config, args[0])
	if err != nil {
		return err
	}

	// TODO: the doc says we support host rename here, but that probably
	// won't work with a live migrate. What do?
	targetd, _, err := flex.NewClient(config, args[1])
	if err != nil {
		return err
	}

	if sourced.Remote == nil || targetd.Remote == nil {
		/*
		 * because we're using rsync, we need to know the "public"
		 * address of this node in order to rsync stuff to it. We
		 * could implement a bunch of code to figure this out, but
		 * since rsync is probably going away anyway, we just disallow
		 * transferring to the "local" remote now. You can work around
		 * this by just adding the public ip address of the local
		 * machine as a remote and connecting to it via http vs. a
		 * unix socket.
		 */
		return fmt.Errorf("checkpointing to local remote not supported")
	}

	result, err := sourced.Checkpoint(name, c.stop, c.verbose)
	if err != nil {
		return err
	}

	// This is a hack right now: we're not returning errors as json, so we
	// just try to convert the thing to an int (i.e. a checkpoint id) and
	// if it fails, it must have been a string error message.
	id, err := strconv.Atoi(result)
	if err != nil {
		return fmt.Errorf("%s", result)
	}

	result, err = sourced.SendContainer(targetd.Remote, name, id)
	if err != nil {
		return err
	}

	fmt.Println(result)

	result, err = targetd.Restore(name, id, c.verbose)
	if err != nil {
		return err
	}

	fmt.Println(result)

	return nil
}
