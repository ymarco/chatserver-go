package main

import (
	"log"
	"time"
)

const (
	Offline OnlineStatus = iota
	Online
)

var userDB = make(map[User]OnlineStatus)

type OnlineStatus int
type UserControl struct {
	onlineStatus OnlineStatus
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

func NewUserHub() UserHub {
	return UserHub{
		logins:           make(chan LoginMessage),
		logouts:          make(chan User),
		broadcastMessage: make(chan Message),
		quit:             make(chan int),
		activeUsers:      make(map[User]UserControl),
	}
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

// UserHub API for chat clients (i.e handleClient). Goroutine-safe.
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
