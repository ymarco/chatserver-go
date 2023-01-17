package main

import (
	"log"
	"time"
)

const (
	Offline OnlineStatus = iota
	Online
)

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
type ReturnCode string

const (
	ReturnOk                 ReturnCode = "Ok"
	ReturnUserAlreadyOnline             = "User already online"
	ReturnUsernameExists                = "Username already exists"
	ReturnInvalidCredentials            = "Wrong username or password"
	ReturnMsgFailedToSome               = "Message failed to send to some users"
	ReturnMsgFailedToAll                = "Message failed to send to any users"
	ReturnInvalidCommand                = "Invalid command"
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
func tryToLogin(newLogin LoginMessage, hub UserHub) {
	switch newLogin.action {
	case ActionLogin:
		pass, exists := hub.userDB[newLogin.user.name]
		if (!exists) || pass != newLogin.user.password {
			newLogin.AckWithCode(ReturnInvalidCredentials)
			return
		} else if _, isActive := hub.activeUsers[newLogin.user]; isActive {
			newLogin.AckWithCode(ReturnUserAlreadyOnline)
			return
		}
	case ActionRegister:
		if _, exists := hub.userDB[newLogin.user.name]; exists {
			newLogin.AckWithCode(ReturnUsernameExists)
			return

		}
	}
	hub.userDB[newLogin.user.name] = newLogin.user.password
	hub.activeUsers[newLogin.user] = newLogin.control
	newLogin.AckWithCode(ReturnOk)
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
	activeUsers   map[User]UserController
	userDB        map[string]string
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
		activeUsers:   make(map[User]UserController),
		userDB:        make(map[string]string),
	}
}

// Manage users. Return only when a message is received from quit. Clients send
// messages to the channels logins, logouts, messageStream and quit, and
// mainHubLoop handles the state and update all the clients.
func mainHubLoop(hub UserHub) {
	for {
		select {
		case newLogin := <-hub.logins:
			tryToLogin(newLogin, hub)
			log.Printf("Logged in: %s\n", newLogin.user.name)
		case logout := <-hub.logouts:
			delete(hub.activeUsers, logout.user)
			logout.Ack()
			log.Printf("Logged out: %s\n", logout.user.name)
		case msg := <-hub.messageStream:
			go sendMessageToAllUsers(msg, copy(hub.activeUsers))
			//log.Printf("%s: %s\n", msg.user.name, msg.content)
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
func copy(m map[User]UserControl) map[User]UserControl {
	new := make(map[User]UserControl)
	for a, b := range m {
		new[a] = b
	}
	return new
}
func tryQuitting(control UserControl, user User) {
	select {
	case control.quit <- struct{}{}:
	case <-time.After(time.Millisecond * 100):
		log.Printf("Failed to send quit user %s\n", user.name)
	}
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
func (hub *UserHub) BroadcastMessage(content string, sender User) ReturnCode {
	m := NewChatMessage(sender, content)
	hub.messageStream <- m
	return m.WaitForAck()
}
func (hub *UserHub) Quit() {
	m := NewMessage(User{})
	hub.quit <- m
	m.WaitForAck()
}

func sendMessageToAllUsers(msg ChatMessage, users map[User]UserControl) {
	totalToSendTo := len(users) - 1
	succeeded := 0
	for user, control := range users {
		if user == msg.user {
			continue
		}

		msg := NewChatMessage(msg.user, msg.content)
		select {
		case control.messages <- msg:
			msg.WaitForAck()
			succeeded++
		case <-time.After(time.Millisecond * 200):
			log.Printf("Failed to send msg to user %s\n", user.name)
		}
	}
	code := ReturnOk
	if succeeded == 0 && totalToSendTo != 0 {
		code = ReturnMsgFailedToAll
	} else if succeeded != totalToSendTo {
		code = ReturnMsgFailedToSome
	}
	msg.AckWithCode(code)
}
