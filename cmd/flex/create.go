package main

import (
	"fmt"
	"github.com/niemeyer/flex"
)

type createCmd struct{}

const createUsage = `
flex create images:ubuntu/$release/$arch

Creates a container using the specified release and arch
`

func (c *createCmd) usage() string {
	return createUsage
}

func (c *createCmd) flags() {}

func (c *createCmd) run(args []string) error {
	if len(args) > 1 {
		return errArgs
	}

	var containerRef string
	if len(args) == 1 {
		containerRef = args[0]
	} else {
		// TODO: come up with a random name a. la. juju/maas
		containerRef = "foo"
	}

	config, err := flex.LoadConfig()
	if err != nil {
		return err
	}

	// NewClient will ping the server to test the connection before returning.
	d, name, err := flex.NewClient(config, containerRef)
	if err != nil {
		return err
	}

	l, err := d.Create(name, "ubuntu", "trusty", "amd64")
	if err == nil {
		fmt.Println(l)
	}
	return err
}
