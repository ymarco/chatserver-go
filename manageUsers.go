package main

import (
	"log"
	"sync"
	"time"
)

type ClientController struct {
	writeMessageToClient chan ChatMessage
}

func NewClientController() *ClientController {
	return &ClientController{writeMessageToClient: make(chan ChatMessage)}
}

type User struct {
	name     string
	password string
}
type Response string

const (
	ResponseOk                 Response = "Ok"
	ResponseUserAlreadyOnline  Response = "User already online"
	ResponseUsernameExists     Response = "Username already exists"
	ResponseInvalidCredentials Response = "Wrong username or password"
	ResponseMsgFailedForSome   Response = "Message failed to send to some users"
	ResponseMsgFailedToAll     Response = "Message failed to send to any users"
	// ResponseIoErrorOccurred should be returned along with a normal error type
	ResponseIoErrorOccurred Response = "IO error, couldn't get a response"
)

var activeUsers = make(map[User]*ClientController)
var activeUsersLock = sync.RWMutex{}

var userDB = make(map[string]string)
var userDBLock = sync.RWMutex{}

func (m *ChatMessage) Ack() {
	// shouldn't block, since the channel has size 1
	m.ack <- ResponseOk
}

func (m *ChatMessage) WaitForAck() Response {
	return <-m.ack
}

func tryToAuthenticate(action AuthAction, user *User, controller *ClientController) Response {
	activeUsersLock.Lock()
	defer activeUsersLock.Unlock()

	userDBLock.Lock()
	defer userDBLock.Unlock()

	switch action {
	case ActionLogin:
		pass, exists := userDB[user.name]
		if (!exists) || pass != user.password {
			return ResponseInvalidCredentials
		} else if _, isActive := activeUsers[*user]; isActive {
			return ResponseUserAlreadyOnline
		}
	case ActionRegister:
		if _, exists := userDB[user.name]; exists {
			return ResponseUsernameExists

		}
	}
	userDB[user.name] = user.password
	activeUsers[*user] = controller
	log.Printf("Logged in: %s\n", user.name)
	return ResponseOk
}
func logout(user *User) {
	userDBLock.Lock()
	defer userDBLock.Unlock()
	delete(activeUsers, *user)
	log.Printf("Logged out: %s\n", user.name)
}

type ChatMessage struct {
	ack     chan Response
	user    *User
	content string
}

func NewChatMessage(user *User, content string) ChatMessage {
	return ChatMessage{make(chan Response, 1), user, content}
}

func copyHashMap(m map[User]*ClientController) map[User]*ClientController {
	res := make(map[User]*ClientController)
	for a, b := range m {
		res[a] = b
	}
	return res
}

func broadcastMessageWait(contents string, sender *User) Response {
	activeUsersLock.RLock()
	cp := copyHashMap(activeUsers)
	activeUsersLock.RUnlock()

	return sendMessageToAllUsersWait(contents, sender, cp)
}

func sendMessageToAllUsersWait(contents string, sender *User, users map[User]*ClientController) Response {
	totalToSendTo := len(users) - 1
	succeeded := 0
	for client, clientController := range users {
		if client == *sender {
			continue
		}

		msg := NewChatMessage(sender, contents)
		select {
		case clientController.writeMessageToClient <- msg:
			msg.WaitForAck()
			succeeded++
		case <-time.After(time.Millisecond * 200):
			log.Printf("Failed to send msg to user %s\n", client.name)
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
