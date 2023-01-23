package main

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

type Response string

const (
	ResponseOk                 Response = "Ok"
	ResponseUserAlreadyOnline  Response = "User already online"
	ResponseUsernameExists     Response = "Username already exists"
	ResponseInvalidCredentials Response = "Wrong username or password"
	ResponseMsgFailedForSome   Response = "Message failed to send to some users"
	ResponseMsgFailedForAll    Response = "Message failed to send to any users"
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
		activeUsers: make(map[UserCredentials]*Client),
		userDB:      make(map[string]string),
	}
}

func (hub *Hub) tryToAuthenticate(request *AuthRequest) (Response, *Client) {
	response := hub.testAuth(request)
	if response == ResponseOk {
		return response, hub.logClientIn(request)
	}
	return response, nil
}
func (hub *Hub) testAuth(request *AuthRequest) Response {
	hub.activeUsersLock.Lock()
	defer hub.activeUsersLock.Unlock()

	hub.userDBLock.Lock()
	defer hub.userDBLock.Unlock()

	switch request.authType {
	case ActionLogin:
		pass, exists := hub.userDB[request.creds.name]
		if !exists || pass != request.creds.password {
			return ResponseInvalidCredentials
		} else if _, isActive := hub.activeUsers[*request.creds]; isActive {
			return ResponseUserAlreadyOnline
		}
		return ResponseOk
	case ActionRegister:
		if _, exists := hub.userDB[request.creds.name]; exists {
			return ResponseUsernameExists
		}
		return ResponseOk
	default:
		panic("unreachable")
	}
}
func (hub *Hub) logClientIn(request *AuthRequest) *Client {
	hub.activeUsersLock.Lock()
	defer hub.activeUsersLock.Unlock()

	hub.userDBLock.Lock()
	defer hub.userDBLock.Unlock()

	client := hub.newClient(request)
	hub.userDB[client.creds.name] = client.creds.password
	hub.activeUsers[*client.creds] = client
	log.Printf("Logged in: %s\n", client.creds.name)
	return client
}
func (hub *Hub) Logout(creds *UserCredentials) {
	hub.activeUsersLock.Lock()
	defer hub.activeUsersLock.Unlock()
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

func NewMessagePipe() (send chan<- ChatMessage, receive <-chan ChatMessage) {
	res := make(chan ChatMessage)
	return res, res
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

	return sendMsgToAllClientsWithTimeout(content, sender, cp)
}

func sendMsgToAllClientsWithTimeout(contents string, sender *UserCredentials, users map[UserCredentials]*Client) Response {
	totalToSendTo := len(users) - 1
	if totalToSendTo == 0 {
		return ResponseOk
	}

	var succeeded int64 = 0
	ctx, cancel := context.WithTimeout(context.Background(), MsgSendTimeout)
	defer cancel() // useless since we ourselves wait on <-Done(), but there's
	// an error otherwise
	for _, client := range users {
		if *client.creds == *sender {
			continue
		}
		go func(client *Client) {
			sendMessageToClient(client, contents, sender, ctx)
			atomic.AddInt64(&succeeded, 1)
		}(client)
	}
	<-ctx.Done()
	if succeeded == 0 {
		return ResponseMsgFailedForAll
	} else if succeeded < int64(totalToSendTo) {
		return ResponseMsgFailedForSome
	} else {
		return ResponseOk
	}
}

var ErrSendingTimedOut = errors.New("couldn't forward message to client: timed out")

const MsgSendTimeout = time.Millisecond * 200

func sendMessageToClient(client *Client, msgContent string,
	sender *UserCredentials, ctx context.Context) {
	msg := NewChatMessage(sender, msgContent)
	select {
	case <-ctx.Done():
	case client.sendMsg <- msg:
		select {
		case <-msg.ack:
		case <-ctx.Done():
		}
	}
}
