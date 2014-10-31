package main

import (
	"github.com/niemeyer/flex"
)

type pingCmd struct {
	httpAddr string
}

const pingUsage = `
flex ping

Pings the flex daemon to check if it is up and working.
`

func (c *pingCmd) usage() string {
	return pingUsage
}

func (c *pingCmd) flags() {}

func (c *pingCmd) run(args []string) error {
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

	// NewClient will ping the server to test the connection before returning.
	_, _, err = flex.NewClient(config, remote)
	return err
}
