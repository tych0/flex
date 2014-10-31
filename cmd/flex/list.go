package main

import (
	"fmt"
	"github.com/niemeyer/flex"
)

type listCmd struct{}

const listUsage = `
flex list

Gets a list of containers from the flex daemon
`

func (c *listCmd) usage() string {
	return listUsage
}

func (c *listCmd) flags() {}

func (c *listCmd) run(args []string) error {
	if len(args) > 1 {
		return errArgs
	}
	config, err := flex.LoadConfig()
	if err != nil {
		return err
	}

	var remote string
	if len(args) == 1 {
		remote = args[0]
	} else {
		remote = config.DefaultRemote
	}

	d, _, err := flex.NewClient(config, remote)
	if err != nil {
		return err
	}
	l, err := d.List()
	if err != nil {
		return err
	}
	fmt.Println(l)
	return err
}
