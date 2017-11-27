package main

import cli "gopkg.in/urfave/cli.v1"

func getCommands() []cli.Command {
	groupsDef := "[group-definition]"
	return []cli.Command{
		{
			Name:  "admin",
			Usage: "administer the skipchain-service",
			Subcommands: []cli.Command{
				{
					Name:    "link",
					Usage:   "link with a skipchain-service",
					Aliases: []string{"l"},
					Action:  adminLink,
					Flags: []cli.Flag{
						cli.StringFlag{
							Name:  "pin, p",
							Value: "",
							Usage: "give ip:port:pin of skipchain-service",
						},
						cli.StringFlag{
							Name:  "private, priv",
							Value: "",
							Usage: "give private.toml of skipchain-service",
						},
					},
				},
				{
					Name:      "auth",
					Usage:     "setting level of authentication",
					Aliases:   []string{"a"},
					ArgsUsage: "ip:port (0|1|2)",
					Action:    adminAuth,
				},
				{
					Name:      "follow",
					Usage:     "follow a skipchain and allow its conodes to set up new skipchains",
					Aliases:   []string{"f"},
					ArgsUsage: "skipchain-id",
					Action:    adminFollow,
				},
				{
					Name:      "unfollow",
					Usage:     "remove a skipchain from the list of followed skipchains",
					Aliases:   []string{"u"},
					ArgsUsage: "skipchain-id",
					Action:    adminUnfollow,
				},
				{
					Name:    "list",
					Usage:   "list all skipchains we follow",
					Aliases: []string{"l"},
					Action:  adminList,
				},
			},
		},
		{
			Name:    "skipchain",
			Usage:   "handle skipchains",
			Aliases: []string{"sc"},
			Subcommands: cli.Commands{
				{
					Name:      "create",
					Usage:     "make a new skipchain",
					Aliases:   []string{"c"},
					ArgsUsage: groupsDef,
					Action:    scCreate,
					Flags: []cli.Flag{
						cli.IntFlag{
							Name:  "base, b",
							Value: 2,
							Usage: "base for skipchains",
						},
						cli.IntFlag{
							Name:  "height, he",
							Value: 2,
							Usage: "maximum height of skipchain",
						},
						cli.StringFlag{
							Name:  "html",
							Usage: "URL of html-skipchain",
						},
					},
				},
				{
					Name:      "add",
					Usage:     "add a new roster to a skipchain",
					Aliases:   []string{"a"},
					ArgsUsage: "skipchain-id " + groupsDef,
					Action:    scAdd,
				},
				{
					Name:      "addWeb",
					Usage:     "add a web-site to a skipchain",
					Aliases:   []string{"a"},
					ArgsUsage: "skipchain-id page.html",
					Action:    scAddWeb,
				},
				{
					Name:      "update",
					Usage:     "get latest valid block",
					Aliases:   []string{"u"},
					ArgsUsage: "skipchain-id",
					Action:    scUpdate,
				},
			},
		},
		{
			Name:  "list",
			Usage: "handle list of skipblocks",
			Subcommands: []cli.Command{
				{
					Name:      "join",
					Usage:     "join a skipchain and store it locally",
					Aliases:   []string{"j"},
					ArgsUsage: groupsDef + " skipchain-id",
					Action:    lsJoin,
				},
				{
					Name:    "known",
					Aliases: []string{"k"},
					Usage:   "lists all known skipblocks",
					Flags: []cli.Flag{
						cli.BoolFlag{
							Name:  "long, l",
							Usage: "give long id of blocks",
						},
					},
					Action: lsKnown,
				},
				{
					Name:      "index",
					Usage:     "create index-files for all known skipchains",
					ArgsUsage: "output path",
					Action:    lsIndex,
				},
				{
					Name:      "fetch",
					Usage:     "ask all known conodes for skipchains",
					ArgsUsage: "[group-file]",
					Flags: []cli.Flag{
						cli.BoolFlag{
							Name:  "recursive, r",
							Usage: "recurse into other conodes",
						},
					},
					Action: lsFetch,
				},
			},
		},
	}
}
