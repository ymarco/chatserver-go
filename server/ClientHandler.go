package server

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	. "util"
)

type ClientHandler struct {
	pendingMsgs <-chan *ChatMessage
	SendMsg     chan<- *ChatMessage
	errs        chan error
	Creds       *UserCredentials
	conn        net.Conn
	hub         *Hub
}

type AuthRequest struct {
	authType AuthAction
	conn     net.Conn
	creds    *UserCredentials
}

func strToAuthAction(str string) (AuthAction, error) {
	switch action := AuthAction(str); action {
	case ActionRegister, ActionLogin:
		return action, nil
	case ActionIOErr: // happens when the client quits without choosing
		return ActionIOErr, ErrClientHasQuit
	default:
		return ActionIOErr, fmt.Errorf("weird output from clientConn: %s", str)
	}
}

func acceptAuthRequest(clientConn net.Conn) (*AuthRequest, error) {
	clientOutput := bufio.NewScanner(clientConn)
	choice, err := ScanLine(clientOutput)
	if err != nil {
		return nil, err
	}
	action, err := strToAuthAction(choice)
	if err != nil {
		return nil, err
	}

	username, err := ScanLine(clientOutput)
	if err != nil {
		return nil, err
	}

	password, err := ScanLine(clientOutput)
	if err != nil {
		return nil, err
	}

	return &AuthRequest{action, clientConn, &UserCredentials{Name: Username(username), Password: Password(password)}}, nil
}
func (hub *Hub) newClientHandler(r *AuthRequest) *ClientHandler {
	sendMsg, receiveMsg := NewMessagePipe()
	errs := make(chan error, 128)
	return &ClientHandler{receiveMsg, sendMsg, errs, r.creds, r.conn, hub}
}
func (handler *ClientHandler) Close() error {
	close(handler.SendMsg)
	return handler.conn.Close()
}

func (hub *Hub) HandleNewConnection(conn net.Conn) {
	defer log.Printf("Disconnected: %s\n", conn.RemoteAddr())
	handler, err := acceptAuthRetry(conn, hub)
	if err != nil {
		if err == ErrClientHasQuit {
			return
		}
		log.Printf("Err with %s: %s", handler.Creds.Name, err)
		return
	}
	defer hub.Logout(handler.Creds.Name)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.sendMsgsLoop(ctx)
	go handler.receivePendingMsgsLoop(ctx)
	err = <-handler.errs
	if err != ErrClientHasQuit {
		log.Println(err)
	}
}

func acceptAuthRetry(clientConn net.Conn, hub *Hub) (*ClientHandler, error) {
	for {
		request, err := acceptAuthRequest(clientConn)
		if err != nil {
			return nil, err
		}

		response, handler := hub.TryToAuthenticate(request)
		if response == ResponseOk {
			return handler, handler.forwardResponseToUser("", ResponseOk)
		}

		// try to communicate that we're retrying
		err = forwardResponseToUser(clientConn, "", response)
		if err != nil {
			log.Printf("Error with %s: %s\n", handler.Creds.Name, err)
			return nil, err
		}
	}
}

func forwardResponseToUser(conn net.Conn, id MsgID, r Response) error {
	_, err := conn.Write([]byte(ServerResponsePrefix + string(id) +
		IdSeparator + string(r) + "\n"))
	return err
}
func (handler *ClientHandler) forwardResponseToUser(id MsgID, r Response) error {
	return forwardResponseToUser(handler.conn, id, r)
}

func (handler *ClientHandler) receivePendingMsgsLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-handler.pendingMsgs:
			handler.forwardMsgToUser(msg)
		}
	}
}

func (handler *ClientHandler) sendMsgsLoop(ctx context.Context) {
	userInput := ReadAsyncIntoChan(bufio.NewScanner(handler.conn))

	for {
		select {
		case <-ctx.Done():
			return
		case input := <-userInput:
			if input.Err != nil {
				handler.errs <- input.Err
				return
			}
			handler.dispatchUserInputAsync(input.Val)
		}
	}
}

func isCommand(s string) bool {
	return strings.HasPrefix(s, cmdPrefix)
}

func parseInputMsg(input string) (id MsgID, msg string, ok bool) {
	if !strings.HasPrefix(input, MsgPrefix) {
		return "", "", false
	}
	input = input[len(MsgPrefix):]
	parts := strings.Split(input, IdSeparator)
	if len(parts) < 2 {
		return "", "", false
	}
	id = MsgID(parts[0])
	msg = input[len(id)+len(IdSeparator):]
	return id, msg, true
}

func (handler *ClientHandler) dispatchUserInputAsync(input string) {
	go func() {
		err := handler.dispatchUserInput(input)
		if err != nil {
			handler.errs <- err
			return
		}
	}()
}
func (handler *ClientHandler) dispatchUserInput(input string) error {
	id, msg, ok := parseInputMsg(input)
	if !ok {
		return ErrOddOutput
	}

	if isCommand(msg) {
		cmd := ToCmd(msg)
		err := handler.forwardResponseToUser(id, ResponseOk)
		if err != nil {
			return err
		}
		return handler.runUserCommand(cmd)
	} else {

		response := handler.hub.BroadcastMessageWithTimeout(msg, handler.Creds.Name)
		return handler.forwardResponseToUser(id, response)
	}
}

func (handler *ClientHandler) runUserCommand(cmd Cmd) error {
	switch cmd {
	case LogoutCmd:
		handler.hub.Logout(handler.Creds.Name)
		return handler.forwardCmdToUser(LogoutCmd)
	default:
		// TODO
		return nil
	}
}

func (handler *ClientHandler) forwardMsgToUser(msg *ChatMessage) {
	_, err := handler.conn.Write([]byte(MsgPrefix + string(msg.sender) + ": " +
		msg.content + "\n"))

	if err != nil {
		handler.errs <- err
		return
	}
	msg.Ack()
	return
}

const cmdPrefix = "/"

func (handler *ClientHandler) forwardCmdToUser(cmd Cmd) error {
	_, err := handler.conn.Write([]byte(cmdPrefix + string(cmd) + "\n"))
	return err
}
