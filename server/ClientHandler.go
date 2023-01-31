package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	. "util"
)

type Broadcaster interface {
	BroadcastMessageWithTimeout(content string, sender Username) Response
}

type ClientHandler struct {
	SendMsg     chan *ChatMessage
	errs        chan error
	relog       chan struct{}
	Creds       *UserCredentials
	clientIn    io.Writer
	clientOut   <-chan ReadInput
	broadcaster Broadcaster
}

type AuthRequest struct {
	authType  AuthAction
	clientIn  io.Writer
	clientOut <-chan ReadInput
	creds     *UserCredentials
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

func acceptAuthRequest(clientIn io.Writer, clientOut <-chan ReadInput) (*AuthRequest, error) {
	choice := <-clientOut
	if choice.Err != nil {
		return nil, choice.Err
	}
	action, err := strToAuthAction(choice.Val)
	if err != nil {
		return nil, err
	}

	username := <-clientOut
	if username.Err != nil {
		return nil, username.Err
	}

	password := <-clientOut
	if password.Err != nil {
		return nil, password.Err
	}

	return &AuthRequest{action, clientIn, clientOut,
		&UserCredentials{Name: Username(username.Val),
			Password: Password(password.Val)}}, nil
}
func newClientHandler(r *AuthRequest, broadcaster Broadcaster) *ClientHandler {
	errs := make(chan error, 128)
	relog := make(chan struct{}, 1)
	sendMsg := make(chan *ChatMessage, 128)
	return &ClientHandler{sendMsg, errs, relog,
		r.creds, r.clientIn, r.clientOut, broadcaster}
}
func (handler *ClientHandler) Close() error {
	close(handler.SendMsg)
	return nil
}

func (hub *Hub) HandleNewConnection(conn net.Conn) {
	defer ClosePrintErr(conn)
	defer log.Printf("Disconnected: %s\n", conn.RemoteAddr())

	clientIn := ReadAsyncIntoChan(bufio.NewScanner(conn))
	shouldRelog := true
	for shouldRelog {
		shouldRelog = hub.handleUntilLoggedOut(conn, clientIn)
	}
}

func (hub *Hub) handleUntilLoggedOut(clientOut io.Writer, clientIn <-chan ReadInput) (expectedToRelog bool) {
	handler, err := hub.acceptAuthRetry(clientOut, clientIn)
	if err != nil {
		if err == ErrClientHasQuit {
			return false
		}
		return false
	}
	defer hub.Logout(handler.Creds.Name)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handler.sendMsgsLoop(ctx)
	go handler.receivePendingMsgsLoop(ctx)
	select {
	case <-handler.relog:
		return true
	case err := <-handler.errs:
		if err == ErrClientHasQuit {
			return false
		} else if err != nil {
			fmt.Println(err)
			return false
		} else {
			panic("unreachable")
		}
	}
}

func (hub *Hub) acceptAuthRetry(clientIn io.Writer, clientOut <-chan ReadInput) (*ClientHandler, error) {
	for {
		request, err := acceptAuthRequest(clientIn, clientOut)
		if err != nil {
			return nil, err
		}

		response, handler := hub.TryToAuthenticate(request)
		if response == ResponseOk {
			return handler, handler.forwardResponseToUser("", ResponseOk)
		}

		// try to communicate that we're retrying
		err = forwardResponseToUser(clientIn, "", response)
		if err != nil {
			log.Printf("Error with %s: %s\n", handler.Creds.Name, err)
			return nil, err
		}
	}
}

func forwardResponseToUser(clientIn io.Writer, id MsgID, r Response) error {
	_, err := clientIn.Write([]byte(ServerResponsePrefix + string(id) +
		IdSeparator + string(r) + "\n"))
	return err
}
func (handler *ClientHandler) forwardResponseToUser(id MsgID, r Response) error {
	return forwardResponseToUser(handler.clientIn, id, r)
}

func (handler *ClientHandler) receivePendingMsgsLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-handler.SendMsg:
			handler.forwardMsgToUser(msg)
		}
	}
}

func (handler *ClientHandler) sendMsgsLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case input := <-handler.clientOut:
			if input.Err != nil {
				handler.errs <- input.Err
				return
			}
			err := handler.dispatchUserInput(input.Val)
			if err != nil {
				handler.errs <- err
				return
			}
		}
	}
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

func (handler *ClientHandler) dispatchUserInput(input string) error {
	id, msg, ok := parseInputMsg(input)
	if !ok {
		return ErrOddOutput
	}

	if IsCmd(msg) {
		return handler.dispatchCmd(UnserializeStrToCmd(msg))
	} else {
		response := handler.broadcaster.BroadcastMessageWithTimeout(msg, handler.Creds.Name)
		return handler.forwardResponseToUser(id, response)
	}
}

func (handler *ClientHandler) dispatchCmd(cmd Cmd) error {
	switch cmd {
	case LogoutCmd:
		handler.relog <- struct{}{}
		return nil
	default:
		// TODO print err
		return nil
	}
}

func (handler *ClientHandler) forwardMsgToUser(msg *ChatMessage) {
	_, err := handler.clientIn.Write([]byte(MsgPrefix + string(msg.sender) + ": " +
		msg.content + "\n"))

	if err != nil {
		handler.errs <- err
		return
	}
	msg.Finish()
	return
}
