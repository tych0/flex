package main

import (
	"fmt"
	"github.com/niemeyer/flex"
)

type byNameCmd struct {
	function string
	do       func(*flex.Client, string) (string, error)
}

func (c *byNameCmd) usage() string {
	return fmt.Sprintf(`
flex %s

Creates a container using the specified release and arch
`, c.function)
}

func (c *byNameCmd) flags() {}

func (c *byNameCmd) run(args []string) error {
	if len(args) != 1 {
		return errArgs
	}

	config, err := flex.LoadConfig()
	if err != nil {
		return err
	}

	d, name, err := flex.NewClient(config, args[0])
	if err != nil {
		return err
	}

	data, err := c.do(d, name)
	fmt.Println(data)
	return err
}
