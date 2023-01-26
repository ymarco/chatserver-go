package main

import (
	"client"
	"fmt"
	"os"
	"server"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Printf("Usage: %s PORT MODE\n\tMODE should be either client or server\n",
			os.Args[0])
		os.Exit(1)
	}
	port, mode := ":"+os.Args[1], os.Args[2]
	switch mode {
	case "client":
		client.RunClient(port, os.Stdin, os.Stdout)
	case "server":
		server.RunServer(port)
	default:
		fmt.Printf("MODE should be client or server, instead got %s\n", os.Args[2])
		os.Exit(1)
	}
}
