package main

import (
	"log"
	"net"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("Usage: %s PORT", os.Args[0])
	}
	port := ":" + os.Args[1]
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
		go handleClient(c, hub)
	}
}
