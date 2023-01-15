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
var usernames = make(map[string]bool)

type OnlineStatus int
type UserControl struct {
	onlineStatus OnlineStatus
	messages     chan ChatMessage
	quit         chan struct{}
}

func NewUserControl() UserControl {
	return UserControl{quit: make(chan struct{}),
		messages: make(chan ChatMessage)}
}

type User struct {
	name     string
	password string
}
type ReturnCode int

const (
	ReturnOk ReturnCode = iota
	ReturnUserAlreadyOnline
	ReturnUsernameExists
	ReturnInvalidCredentials
)

type Message struct {
	ack  chan ReturnCode
	user User
}

func NewMessage(user User) Message {
	return Message{make(chan ReturnCode, 1), user}
}

func (m *Message) Ack() {
	// shouldn't block, since the channel has size 1
	m.ack <- ReturnOk
}
func (m *Message) AckWithCode(code ReturnCode) {
	m.ack <- code
}
func (m *Message) WaitForAck() ReturnCode {
	return <-m.ack
}

type LoginMessage struct {
	Message
	control UserControl
	action  LoginAction
}

func NewLoginMessage(user User, control UserControl, action LoginAction) LoginMessage {
	return LoginMessage{
		NewMessage(user),
		control,
		action,
	}
}

type LogoutMessage struct {
	Message
}

func NewLogoutMessage(user User) LogoutMessage {
	return LogoutMessage{NewMessage(user)}
}

type ChatMessage struct {
	Message
	content string
}

func NewChatMessage(user User, content string) ChatMessage {
	return ChatMessage{NewMessage(user), content}
}

type UserHub struct {
	activeUsers   map[User]UserControl
	logins        chan LoginMessage
	logouts       chan LogoutMessage
	messageStream chan ChatMessage
	quit          chan Message
}

func NewUserHub() UserHub {
	return UserHub{
		logins:        make(chan LoginMessage, 256),
		logouts:       make(chan LogoutMessage, 256),
		messageStream: make(chan ChatMessage, 256),
		quit:          make(chan Message, 256),
		activeUsers:   make(map[User]UserControl),
	}
}

// Manage users. Return only when a message is received from quit. Clients send
// messages to the channels logins, logouts, messageStream and quit, and
// mainHubLoop handles the state and update all the clients.
func mainHubLoop(hub UserHub) {
	for {
		select {
		case newLogin := <-hub.logins:
			// TODO check
			switch newLogin.action {
			case ActionLogin:
				if _, exists := userDB[newLogin.user]; !exists {
					newLogin.AckWithCode(ReturnInvalidCredentials)
					continue
				} else if userDB[newLogin.user] == Online {
					newLogin.AckWithCode(ReturnUserAlreadyOnline)
					continue
				}
			case ActionRegister:
				if usernames[newLogin.user.name] {
					newLogin.AckWithCode(ReturnUsernameExists)
					continue
				}
			}
			userDB[newLogin.user] = Online
			hub.activeUsers[newLogin.user] = newLogin.control
			newLogin.AckWithCode(ReturnOk)
			log.Printf("Logged in: %s\n", newLogin.user.name)
		case logout := <-hub.logouts:
			delete(hub.activeUsers, logout.user)
			userDB[logout.user] = Offline
			logout.Ack()
			log.Printf("Logged out: %s\n", logout.user.name)
		case msg := <-hub.messageStream:
			go broadcastMessage(msg, hub.activeUsers)
			log.Printf("%s: %s\n", msg.user.name, msg.content)
		case q := <-hub.quit:
			for user, control := range hub.activeUsers {
				go tryQuitting(control, user)
			}
			q.Ack()
			log.Println("Quitting")
			return
		}
	}
}

func tryQuitting(control UserControl, user User) {
	select {
	case control.quit <- struct{}{}:
	case <-time.After(time.Millisecond * 100):
		log.Printf("Failed to send quit user %s\n", user.name)
	}
}

func trySendingMessage(recipient User, recipientControl UserControl, msg ChatMessage) {
}

func (hub *UserHub) Login(user User, action LoginAction, control UserControl) ReturnCode {
	m := NewLoginMessage(user, control, action)
	hub.logins <- m
	return m.WaitForAck()
}
func (hub *UserHub) Logout(user User) {
	m := NewLogoutMessage(user)
	hub.logouts <- m
	m.WaitForAck()
}
func (hub *UserHub) SendMessage(content string, sender User) ReturnCode {
	m := NewChatMessage(sender, content)
	hub.messageStream <- m
	return m.WaitForAck()
}
func (hub *UserHub) Quit() {
	m := NewMessage(User{})
	hub.quit <- m
	m.WaitForAck()
}

func broadcastMessage(msg ChatMessage, users map[User]UserControl) {
	for user, control := range users {
		if user == msg.user {
			continue
		}

		msg := NewChatMessage(msg.user, msg.content)
		select {
		case control.messages <- msg:
			msg.WaitForAck()
		case <-time.After(time.Millisecond * 200):
			log.Printf("Failed to send msg to user %s\n", user.name)
		}
	}
	msg.Ack() // TODO send how many users the message reached
}
