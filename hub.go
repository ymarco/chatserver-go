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
type UserController struct {
	messages chan ChatMessage
	quit     chan struct{}
}

func NewUserController() UserController {
	return UserController{quit: make(chan struct{}),
		messages: make(chan ChatMessage)}
}

type User struct {
	name     string
	password string
}
type Response string

const (
	ResponseOk                 Response = "Ok"
	ResponseUserAlreadyOnline           = "User already online"
	ResponseUsernameExists              = "Username already exists"
	ResponseInvalidCredentials          = "Wrong username or password"
	ResponseMsgFailedForSome            = "Message failed to send to some users"
	ResponseMsgFailedToAll              = "Message failed to send to any users"
)

type Message struct {
	ack  chan ReturnCode
	user User
}

func NewMessage(user User) Message {
	return Message{make(chan Response, 1), user}
}

func (m *Message) Ack() {
	// shouldn't block, since the channel has size 1
	m.ack <- ResponseOk
}
func (m *Message) AckWithCode(code Response) {
	m.ack <- code
}
func (m *Message) WaitForAck() Response {
	return <-m.ack
}

type LoginMessage struct {
	Message
	controller UserController
	action     LoginAction
}

func NewLoginMessage(user User, controller UserController, action LoginAction) LoginMessage {
	return LoginMessage{
		NewMessage(user),
		controller,
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
			for user, controller := range hub.activeUsers {
				go tryQuitting(controller, user)
			}
			q.Ack()
			log.Println("Quitting")
			return
		}
	}
}
func copy(m map[User]UserController) map[User]UserController {
	new := make(map[User]UserController)
	for a, b := range m {
		new[a] = b
	}
	return new
}
func tryQuitting(controller UserController, user User) {
	select {
	case controller.quit <- struct{}{}:
	case <-time.After(time.Millisecond * 100):
		log.Printf("Failed to send quit user %s\n", user.name)
	}
}

func (hub *UserHub) Login(user User, action LoginAction, controller UserController) Response {
	m := NewLoginMessage(user, controller, action)
	hub.logins <- m
	return m.WaitForAck()
}
func (hub *UserHub) Logout(user User) {
	m := NewLogoutMessage(user)
	hub.logouts <- m
	m.WaitForAck()
}
func (hub *UserHub) BroadcastMessage(content string, sender User) Response {
	m := NewChatMessage(sender, content)
	hub.messageStream <- m
	return m.WaitForAck()
}
func (hub *UserHub) Quit() {
	m := NewMessage(User{})
	hub.quit <- m
	m.WaitForAck()
}

func sendMessageToAllUsers(msg ChatMessage, users map[User]UserController) {
	totalToSendTo := len(users) - 1
	succeeded := 0
	for user, controller := range users {
		if user == msg.user {
			continue
		}

		msg := NewChatMessage(msg.user, msg.content)
		select {
		case controller.messages <- msg:
			msg.WaitForAck()
			succeeded++
		case <-time.After(time.Millisecond * 200):
			log.Printf("Failed to send msg to user %s\n", user.name)
		}
	}
	code := ResponseOk
	if succeeded == 0 && totalToSendTo != 0 {
		code = ResponseMsgFailedToAll
	} else if succeeded != totalToSendTo {
		code = ResponseMsgFailedForSome
	}
	msg.AckWithCode(code)
}
