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

func PromptForUsernameAndPassword(c net.Conn) (e error, response int) {
	_, err := c.Write([]byte("Type r to register, l to login\n"))
	if err != nil {
		return err, Error
	}
	reader := bufio.NewReader(c)

	s, err := reader.ReadString('\n')

	if err != nil {
		return err, Error
	}
	switch s {
	case "l\n":
		return nil, WantsToLogIn
	case "r\n":
		return nil, WantsToRegister
	default:
		return nil, Retry
	}
}

func handleClient(c net.Conn, hub UserHub) {
	_, err := c.Write([]byte("Connected successfully\n"))
	if err != nil {
		log.Printf("Error writing: %s", err)
		return
	}

	user, err := RegisterOrTryLoggingIn(c)
	if err == io.EOF {
		return
	} else if err != nil {
		log.Printf("Error: %s", err)
		return
	}

	control := NewUserControl()
	hub.Login(user, control)
	defer hub.Logout(user)
	c.Write([]byte("Logged in as "))
	c.Write([]byte(user.name))
	c.Write([]byte("\n\n"))

	MainClientLoop(c, hub, user, control)
}

func MainClientLoop(c net.Conn, hub UserHub, user User, control UserControl) {
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
			printMsg(c, msg)
		}
	}
}

func printMsg(writer io.Writer, msg Message) {
	writer.Write([]byte(msg.sender.name))
	writer.Write([]byte(": "))
	writer.Write([]byte(msg.msg))
}

func RegisterOrTryLoggingIn(c net.Conn) (User, error) {
	user := User{}
retry:
	for {
		err, response := PromptForUsernameAndPassword(c)
		if err != nil {
			return User{}, err
		}
		switch response {
		case Retry:
			continue retry
		case WantsToLogIn:
			fallthrough
		case WantsToRegister:
			user, err = promptUsernameAndPassword(c)
			if err != nil {
				return User{}, err
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
	// success
	return user, nil
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
		return User{}, err
	}
	name = name[0 : len(name)-1]
	c.Write([]byte("Password: "))
	pass, err := r.ReadString('\n')
	if err != nil {
		return User{}, err
	}
	pass = pass[0 : len(pass)-1]

	return User{name, pass}, nil
}
