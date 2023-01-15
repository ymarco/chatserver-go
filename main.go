package main

import (
	"fmt"
	"log"
	"net"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Printf("Usage: %s PORT MODE\n\tMODE should be either client or server\n",
			os.Args[0])
		os.Exit(1)
	}
	port := ":" + os.Args[1]
	switch os.Args[2] {
	case "client":
		client(port)
	case "server":
		server(port)
	default:
		fmt.Printf("MODE should be client or server, instead got %s\n", os.Args[2])
		os.Exit(1)
	}
}
func server(port string) {
	l, err := net.Listen("tcp4", port)
	if err != nil {
		log.Fatalln(err)
	}
	defer l.Close()
	hub := NewUserHub()
	go manageUsers(hub)
	defer hub.Quit()
	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatalln(err)
		}
		log.Printf("Got a connection from %s\n", c.LocalAddr().String())
		go handleClient(c, hub)
	}
}
