package main

import (
	"fmt"
	"os"

	"github.com/kameshsampath/drone-provenance/pkg/drone"
	"github.com/urfave/cli/v2"
)

// drone version number
var version string

func main() {
	fmt.Println("Jai Guru!")
	app := cli.NewApp()
	app.Name = "drone"
	app.Version = version
	app.Usage = "command line utility to run drone pipelines locally"
	app.EnableBashCompletion = true

	app.Commands = []*cli.Command{
		drone.Command,
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

}
