package main

import (
	"os"

	"github.com/momhq/mom/ingress/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
