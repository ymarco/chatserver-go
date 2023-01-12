package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"
)

const (
	Offline = iota
	Online
)

var userDB map[User]int = make(map[User]int)

const (
	Register = iota
	Login
)

type UserControl struct {
	onlineStatus int
	messages     chan Message
	quit         chan int
}
type User struct {
	name     string
	password string
}

type LoginMessage struct {
	user    User
	control UserControl
}
type Message struct {
	msg    string
	sender User
}
type UserHub struct {
	activeUsers      map[User]UserControl
	logins           chan LoginMessage
	logouts          chan User
	broadcastMessage chan Message
	quit             chan int
}

// Manage users. Return only when a message is received from quit. Clients send
// messages to the channels logins, logouts, broadcastMessage and quit, and
// manageUsers handles the state and update all the clients.
func manageUsers(hub UserHub) {
	for {
		select {
		case newLogin := <-hub.logins:
			hub.activeUsers[newLogin.user] = newLogin.control
			userDB[newLogin.user] = Online
		case user := <-hub.logouts:
			delete(hub.activeUsers, user)
			userDB[user] = Offline
		case msg := <-hub.broadcastMessage:
			go func() {
				for user, control := range hub.activeUsers {
					select {
					case control.messages <- msg:
					case <-time.After(time.Millisecond * 100):
						log.Printf("Failed to send msg to user %s\n", user.name)
					}
				}
			}()
		case <-hub.quit:
			for user, control := range hub.activeUsers {
				select {
				case control.quit <- 1:
				case <-time.After(time.Millisecond * 100):
					log.Printf("Failed to send quit user %s\n", user.name)
				}
			}
			return
		}
	}
}

// UserHub API for chat clients (i.e handleConnection). Thread-safe.
func (hub *UserHub) Login(user User, control UserControl) {
	hub.logins <- LoginMessage{user, control}
}
func (hub *UserHub) Logout(user User) {
	hub.logouts <- user
}
func (hub *UserHub) SendMessage(msg string, sender User) {
	hub.broadcastMessage <- Message{msg, sender}
}
func (hub *UserHub) Quit() {
	hub.quit <- 1
}

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
	hub := UserHub{
		logins:           make(chan LoginMessage),
		logouts:          make(chan User),
		broadcastMessage: make(chan Message),
		quit:             make(chan int),
		activeUsers:      make(map[User]UserControl),
	}
	go manageUsers(hub)
	defer hub.Quit()
	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatalln(err)
		}
		go handleConnection(c, hub)
	}
}

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

func handleConnection(c net.Conn, hub UserHub) {
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
			log.Printf("userDB:%s\n", userDB)
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
