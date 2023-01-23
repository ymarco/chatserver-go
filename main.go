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
	port, mode := ":"+os.Args[1], os.Args[2]
	switch mode {
	case "client":
		client(port, os.Stdin, os.Stdout)
	case "server":
		server(port)
	default:
		fmt.Printf("MODE should be client or server, instead got %s\n", os.Args[2])
		os.Exit(1)
	}
}

type Closer interface {
	Close() error
}

func closePrintErr(c Closer) {
	err := c.Close()
	if err != nil {
		log.Println(err)
	}
}

func server(port string) {
	listener, err := net.Listen("tcp4", port)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("Listening at %s\n", listener.Addr())
	defer closePrintErr(listener)
	hub := NewHub()
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatalln(err)
		}
		log.Printf("Connected: %s\n", conn.RemoteAddr())
		go hub.handleNewConnection(conn)
	}
}
