package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
)

const (
	WantsToLogIn = iota
	WantsToRegister
	Retry
	Error
)

func promptForLogin(c net.Conn) (e error, response int) {
	_, err := c.Write([]byte("Type r to register, l to login\n"))
	if err != nil {
		return err, Error
	}
	reader := bufio.NewReader(c)

	s, err := reader.ReadString('\n')

	if err != nil {
		return err, Error
	}
	if s == "l\n" {
		return nil, WantsToLogIn
	} else if s == "r\n" {
		return nil, WantsToRegister
	} else {
		return nil, Retry
	}
}

func handleClient(c net.Conn, hub UserHub) {
	// assure user that they connected
	_, err := c.Write([]byte("Connected successfully\n"))
	if err != nil {
		log.Printf("Error writing: %s", err)
		return
	}
	// try logging in or registering
	user := User{"", ""}
retry:
	for {
		err, response := promptForLogin(c)
		if err == io.EOF {
			return
		} else if err != nil {
			log.Printf("Error: %s", err)
			return
		}
		switch response {
		case Retry:
			continue retry
		case WantsToLogIn:
			fallthrough
		case WantsToRegister:
			user, err = promptUsernameAndPassword(c)
			if err == io.EOF {
				return
			} else if err != nil {
				log.Printf("Error logging in: %s\n", err)
				return
			}
			status, exists := userDB[user]
			if response == WantsToLogIn && !exists {
				c.Write([]byte("Can't login: invalid credentials\n"))
				continue retry
			} else if response == WantsToRegister && exists {
				c.Write([]byte("Can't register: user exists\n"))
				continue retry
			} else if status == Online {
				c.Write([]byte("Can't login: user already online\n"))
				continue retry
			}
			break retry
		}
	}
	// log in
	control := UserControl{quit: make(chan int),
		messages: make(chan Message)}
	hub.Login(user, control)
	defer hub.Logout(user)
	c.Write([]byte("Logged in as "))
	c.Write([]byte(user.name))
	c.Write([]byte("\n\n"))

	// user is logged in; main loop.
	userInput := make(chan string)
	eof := make(chan int)
	go readIntoChan(c, userInput, eof)
loop:
	for {
		select {
		case line := <-userInput:
			hub.SendMessage(line, user)
		case <-eof:
			// user disconnected
			break loop
		case <-control.quit:
			break loop
		case msg := <-control.messages:
			c.Write([]byte(msg.sender.name))
			c.Write([]byte(": "))
			c.Write([]byte(msg.msg))
		}
	}
}

// Read lines from reader and send them to outputs.
func readIntoChan(reader io.Reader, outputs chan string, eof chan int) {
	r := bufio.NewReader(reader)
	for {
		line, err := r.ReadString('\n')
		if err == io.EOF {
			eof <- 1
			return
		} else if err != nil {
			fmt.Printf("reading error: %v", err)
			return
		}
		outputs <- line
	}

}

func promptUsernameAndPassword(c net.Conn) (User, error) {
	r := bufio.NewReader(c)
	c.Write([]byte("Username: "))
	name, err := r.ReadString('\n')

	if err != nil {
		return User{"", ""}, err
	}
	name = name[0 : len(name)-1]
	c.Write([]byte("Password: "))
	pass, err := r.ReadString('\n')
	if err != nil {
		return User{"", ""}, err
	}
	pass = pass[0 : len(pass)-1]

	return User{name, pass}, nil
}
