package main

import (
	"log"
	"sync"
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

var activeUsers = make(map[User]UserController)
var activeUsersLock = sync.Mutex{}
var userDB = make(map[string]string)
var userDBLock = sync.Mutex{}

type Message struct {
	ack  chan Response
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

func tryToLogin(action AuthAction, user User, controller UserController) Response {
	activeUsersLock.Lock()
	defer activeUsersLock.Unlock()

	userDBLock.Lock()
	defer userDBLock.Unlock()

	switch action {
	case ActionLogin:
		pass, exists := userDB[user.name]
		if (!exists) || pass != user.password {
			return ResponseInvalidCredentials
		} else if _, isActive := activeUsers[user]; isActive {
			return ResponseUserAlreadyOnline
		}
	case ActionRegister:
		if _, exists := userDB[user.name]; exists {
			return ResponseUsernameExists

		}
	}
	userDB[user.name] = user.password
	activeUsers[user] = controller
	return ResponseOk
}
func logout(user User) {
	userDBLock.Lock()
	defer activeUsersLock.Unlock()
	delete(activeUsers, user)
	log.Printf("Logged out: %s\n", user.name)
}

type ChatMessage struct {
	Message
	content string
}

func NewChatMessage(user User, content string) ChatMessage {
	return ChatMessage{NewMessage(user), content}
}

type UserHub struct {
	quit chan Message
}

func NewUserHub() UserHub {
	return UserHub{
		quit: make(chan Message, 256),
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

func broadcastMessageWait(contents string, sender User) Response {
	activeUsersLock.Lock()
	cp := copy(activeUsers)
	activeUsersLock.Unlock()

	return sendMessageToAllUsersWait(contents, sender, cp)
}

func sendMessageToAllUsersWait(contents string, sender User, users map[User]UserController) Response {
	totalToSendTo := len(users) - 1
	succeeded := 0
	for user, controller := range users {
		if user == sender {
			continue
		}

		msg := NewChatMessage(sender, contents)
		select {
		case controller.messages <- msg:
			msg.WaitForAck()
			succeeded++
		case <-time.After(time.Millisecond * 200):
			log.Printf("Failed to send msg to user %s\n", user.name)
		}
	}
	if succeeded == 0 && totalToSendTo != 0 {
		return ResponseMsgFailedToAll
	} else if succeeded != totalToSendTo {
		return ResponseMsgFailedForSome
	} else {
		return ResponseOk
	}
}
