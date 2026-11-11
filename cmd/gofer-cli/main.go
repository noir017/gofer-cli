package main

import (
	"os"

	"github.com/noir017/gofer-cli/internal/cli"
)

func main() { os.Exit(cli.Execute(os.Args[1:])) }
