package server

import (
	"context"
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
	activeUsers     map[Username]*ClientHandler
	activeUsersLock sync.RWMutex

	userDB     map[Username]Password
	userDBLock sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		activeUsers: make(map[Username]*ClientHandler),
		userDB:      make(map[Username]Password),
	}
}

func (hub *Hub) TryToAuthenticate(request *AuthRequest) (Response, *ClientHandler) {
	response := hub.testAuth(request)
	if response != ResponseOk {
		return response, nil
	}
	return response, hub.logClientIn(request)
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
		} else if _, isActive := hub.activeUsers[request.creds.Name]; isActive {
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
func (hub *Hub) logClientIn(request *AuthRequest) *ClientHandler {
	hub.activeUsersLock.Lock()
	defer hub.activeUsersLock.Unlock()

	hub.userDBLock.Lock()
	defer hub.userDBLock.Unlock()

	client := hub.newClientHandler(request)
	hub.userDB[client.Creds.Name] = client.Creds.Password
	hub.activeUsers[client.Creds.Name] = client
	log.Printf("Logged in: %s\n", client.Creds.Name)
	return client
}
func (hub *Hub) Logout(name Username) {
	hub.activeUsersLock.Lock()
	defer hub.activeUsersLock.Unlock()

	ClosePrintErr(hub.activeUsers[name])
	delete(hub.activeUsers, name)
	log.Printf("Logged out: %s\n", name)
}

type ChatMessage struct {
	finished chan struct{}
	sender   Username
	content  string
}

func NewChatMessage(sender Username, content string) *ChatMessage {
	return &ChatMessage{make(chan struct{}, 1), sender, content}
}

func (m *ChatMessage) Finish() {
	// shouldn't block, since the channel has size 1
	m.finished <- struct{}{}
}

func (m *ChatMessage) WaitForAck() {
	<-m.finished
}

func NewMessagePipe() (send chan<- *ChatMessage, receive <-chan *ChatMessage) {
	res := make(chan *ChatMessage)
	return res, res
}

func (hub *Hub) BroadcastMessageWithTimeout(content string, sender Username) Response {
	hub.activeUsersLock.RLock()
	totalToSendTo := len(hub.activeUsers) - 1
	if totalToSendTo == 0 {
		hub.activeUsersLock.RUnlock()
		return ResponseOk
	}
	errs := make(chan error, totalToSendTo)
	ctx, cancel := context.WithTimeout(context.Background(), MsgSendTimeout)
	defer cancel()

	for _, client := range hub.activeUsers {
		if client.Creds.Name == sender {
			continue
		}
		go func(handler *ClientHandler) {
			errs <- sendMessageToClient(handler, content, sender, ctx)
		}(client)
	}
	hub.activeUsersLock.RUnlock()
	succeeded := 0
	// a range on errs would cause a hang here since we don't close the channel
	for i := 0; i < totalToSendTo; i++ {
		if err := <-errs; err != nil {
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

func sendMessageToClient(recipient *ClientHandler, content string,
	sender Username, ctx context.Context) error {
	msg := NewChatMessage(sender, content)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case recipient.SendMsg <- msg:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-msg.finished:
	}
	return nil
}
