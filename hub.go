package main

import (
	"log"
	"sync"
	"time"
)

func NewMessagePipe() (send chan<- ChatMessage, receive <-chan ChatMessage) {
	res := make(chan ChatMessage)
	return res, res
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

type Hub struct {
	activeUsers     map[UserCredentials]*Client
	activeUsersLock sync.RWMutex

	userDB     map[string]string
	userDBLock sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		make(map[UserCredentials]*Client),
		sync.RWMutex{},
		make(map[string]string),
		sync.RWMutex{},
	}
}

func (hub *Hub) TryToAuthenticate(action AuthAction, client *Client) Response {
	hub.activeUsersLock.Lock()
	defer hub.activeUsersLock.Unlock()

	hub.userDBLock.Lock()
	defer hub.userDBLock.Unlock()

	switch action {
	case ActionLogin:
		pass, exists := hub.userDB[client.creds.name]
		if !exists || pass != client.creds.password {
			return ResponseInvalidCredentials
		} else if _, isActive := hub.activeUsers[*client.creds]; isActive {
			return ResponseUserAlreadyOnline
		}
	case ActionRegister:
		if _, exists := hub.userDB[client.creds.name]; exists {
			return ResponseUsernameExists
		}
	default:
		panic("unreachable")
	}
	hub.userDB[client.creds.name] = client.creds.password
	hub.activeUsers[*client.creds] = client
	log.Printf("Logged in: %s\n", client.creds.name)
	return ResponseOk
}
func (hub *Hub) Logout(creds *UserCredentials) {
	hub.userDBLock.Lock()
	defer hub.userDBLock.Unlock()
	delete(hub.activeUsers, *creds)
	log.Printf("Logged out: %s\n", creds.name)
}

type ChatMessage struct {
	ack     chan struct{}
	sender  *UserCredentials
	content string
}

func NewChatMessage(user *UserCredentials, content string) ChatMessage {
	return ChatMessage{make(chan struct{}, 1), user, content}
}

func (m *ChatMessage) Ack() {
	// shouldn't block, since the channel has size 1
	m.ack <- struct{}{}
}

func (m *ChatMessage) WaitForAck() {
	<-m.ack
}

func copyHashMap(m map[UserCredentials]*Client) map[UserCredentials]*Client {
	res := make(map[UserCredentials]*Client)
	for a, b := range m {
		res[a] = b
	}
	return res
}

func (hub *Hub) broadcastMessageWait(content string, sender *UserCredentials) Response {
	hub.activeUsersLock.RLock()
	cp := copyHashMap(hub.activeUsers)
	hub.activeUsersLock.RUnlock()

	return sendMessageToAllClientsWait(content, sender, cp)
}

func sendMessageToAllClientsWait(contents string, sender *UserCredentials, users map[UserCredentials]*Client) Response {
	totalToSendTo := len(users) - 1
	succeeded := 0

	go func() {
		// for
		// go Send message to user
	}()

	for _, client := range users {
		if *client.creds == *sender {
			continue
		}

		msg := NewChatMessage(sender, contents)

		select {
		case client.sendMsg <- msg:
			select {
			case <-msg.ack:
				succeeded++
			case <-time.After(time.Millisecond * 200):
				log.Printf("Failed to send msg to user %s\n", client.creds.name)
			}
		case <-time.After(time.Millisecond * 200):
			log.Printf("Failed to send msg to user %s\n", client.creds.name)
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
