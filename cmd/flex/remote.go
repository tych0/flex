package main

import (
	"fmt"
	"github.com/niemeyer/flex"
)

type remoteCmd struct {
	httpAddr string
}

const remoteUsage = `
flex remote add <name> <url>         Add the remote <name> at <url>.
flex remote rm <name>                Remove the remote <name>.
flex remote list                     List all remotes.
flex remote rename <old> <new>       Rename remote <old> to <new>.
flex remote set-url <name> <url>     Update <name>'s url to <url>.

Manage remote flex servers.
`

func (c *remoteCmd) usage() string {
	return remoteUsage
}

func (c *remoteCmd) flags() {}

func (c *remoteCmd) run(args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	config, err := flex.LoadConfig()
	if err != nil {
		return err
	}

	switch args[0] {
	case "add":
		if len(args) != 3 {
			return errArgs
		}

		if rc, ok := config.Remotes[args[1]]; ok {
			return fmt.Errorf("remote %s exists as <%s>", args[1], rc.Addr)
		}

		if config.Remotes == nil {
			config.Remotes = make(map[string]flex.RemoteConfig)
		}
		config.Remotes[args[1]] = flex.RemoteConfig{args[2]}
	case "rm":
		if len(args) != 2 {
			return errArgs
		}

		if _, ok := config.Remotes[args[1]]; !ok {
			return fmt.Errorf("remote %s doesn't exist", args[1])
		}

		delete(config.Remotes, args[1])
	case "list":
		for name, rc := range config.Remotes {
			fmt.Println(fmt.Sprintf("%s <%s>", name, rc.Addr))
		}
		/* Here, we don't need to save since we didn't actually modify
		 * anything, so just return. */
		return nil
	case "rename":
		if len(args) != 3 {
			return errArgs
		}

		rc, ok := config.Remotes[args[1]]
		if !ok {
			return fmt.Errorf("remote %s doesn't exist", args[1])
		}

		if _, ok := config.Remotes[args[2]]; ok {
			return fmt.Errorf("remote %s already exists", args[2])
		}

		config.Remotes[args[2]] = rc
		delete(config.Remotes, args[1])
	case "set-url":
		if len(args) != 3 {
			return errArgs
		}
		_, ok := config.Remotes[args[1]]
		if !ok {
			return fmt.Errorf("remote %s doesn't exist", args[1])
		}
		config.Remotes[args[1]] = flex.RemoteConfig{args[2]}
	}

	return flex.SaveConfig(config)
}
