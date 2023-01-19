package main

import (
	"log"
	"sync"
	"time"
)

func NewMessageChannel() (send chan<- ChatMessage, recieve <-chan ChatMessage) {
	res := make(chan ChatMessage)
	return res, res
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

var activeUsers = make(map[User]chan<- ChatMessage)
var activeUsersLock = sync.RWMutex{}

var userDB = make(map[string]string)
var userDBLock = sync.RWMutex{}

func tryToAuthenticate(action AuthAction, user *User,
	sendMessage chan<- ChatMessage) Response {
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
	activeUsers[*user] = sendMessage
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
	ack     chan struct{}
	sender  *User
	content string
}

func NewChatMessage(user *User, content string) ChatMessage {
	return ChatMessage{make(chan struct{}, 1), user, content}
}

func (m *ChatMessage) Ack() {
	// shouldn't block, since the channel has size 1
	m.ack <- struct{}{}
}

func (m *ChatMessage) WaitForAck() {
	<-m.ack
}

func copyHashMap(m map[User]chan<- ChatMessage) map[User]chan<- ChatMessage {
	res := make(map[User]chan<- ChatMessage)
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

func sendMessageToAllUsersWait(contents string, sender *User, users map[User]chan<- ChatMessage) Response {
	totalToSendTo := len(users) - 1
	succeeded := 0
	for client, sendMessage := range users {
		if client == *sender {
			continue
		}

		msg := NewChatMessage(sender, contents)
		select {
		case sendMessage <- msg:
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
