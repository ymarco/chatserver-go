package server

import (
	"context"
	"errors"
	"log"
	"net"
	"sync"
	. "util"
)

func RunServer(port string) {
	listener, err := net.Listen("tcp4", port)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("Listening at %s\n", listener.Addr())
	defer ClosePrintErr(listener)
	hub := NewHub()
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatalln(err)
		}
		log.Printf("Connected: %s\n", conn.RemoteAddr())
		go hub.HandleNewConnection(conn)
	}
}

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

func (hub *Hub) TryToAuthenticate(request *AuthRequest) (Response, *Client) {
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
		pass, exists := hub.userDB[request.creds.Name]
		if !exists || pass != request.creds.Password {
			return ResponseInvalidCredentials
		} else if _, isActive := hub.activeUsers[*request.creds]; isActive {
			return ResponseUserAlreadyOnline
		}
		return ResponseOk
	case ActionRegister:
		if _, exists := hub.userDB[request.creds.Name]; exists {
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
	hub.userDB[client.Creds.Name] = client.Creds.Password
	hub.activeUsers[*client.Creds] = client
	log.Printf("Logged in: %s\n", client.Creds.Name)
	return client
}
func (hub *Hub) Logout(creds *UserCredentials) {
	hub.activeUsersLock.Lock()
	defer hub.activeUsersLock.Unlock()
	delete(hub.activeUsers, *creds)
	log.Printf("Logged out: %s\n", creds.Name)
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

func (hub *Hub) BroadcastMessageWait(content string, sender *UserCredentials) Response {
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
	errors := make(chan error, totalToSendTo)
	ctx, cancel := context.WithTimeout(context.Background(), MsgSendTimeout)
	defer cancel()

	for _, client := range users {
		if *client.Creds == *sender {
			continue
		}
		go func(client *Client) {
			errors <- sendMessageToClient(client, contents, sender, ctx)
		}(client)
	}
	succeeded := 0
	for i := 0; i < totalToSendTo; i++ {
		if err := <-errors; err != nil {
			log.Printf("Error sending msg: %s\n", err)
		} else {
			succeeded++
		}
	}
	if succeeded == 0 {
		return ResponseMsgFailedForAll
	} else if succeeded < totalToSendTo {
		return ResponseMsgFailedForSome
	} else {
		return ResponseOk
	}
}

var ErrSendingTimedOut = errors.New("couldn't forward message to client: timed out")

func sendMessageToClient(client *Client, content string,
	sender *UserCredentials, ctx context.Context) error {
	msg := NewChatMessage(sender, content)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case client.SendMsg <- msg:
		select {
		case <-msg.ack:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
