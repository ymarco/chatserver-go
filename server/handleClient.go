package server

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	. "util"
)

type Client struct {
	pendingMsgs <-chan ChatMessage
	SendMsg     chan<- ChatMessage
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

	return &AuthRequest{action, clientConn, &UserCredentials{username, password}}, nil
}
func (hub *Hub) newClient(r *AuthRequest) *Client {
	sendMsg, receiveMsg := NewMessagePipe()
	errs := make(chan error, 128)
	return &Client{receiveMsg, sendMsg, errs, r.creds, r.conn, hub}
}
func (client *Client) Close() error {
	close(client.SendMsg)
	return client.conn.Close()
}

func (hub *Hub) HandleNewConnection(conn net.Conn) {
	defer log.Printf("Disconnected: %s\n", conn.RemoteAddr())
	client, err := acceptAuthRetry(conn, hub)
	if err == ErrClientHasQuit {
		return
	} else if err != nil {
		log.Printf("Err with %s: %s", client.Creds.Name, err)
		return
	}
	defer hub.Logout(client.Creds)

	err = client.handleMessagesLoop()
	if err != ErrClientHasQuit {
		log.Println(err)
	}
}

func acceptAuthRetry(clientConn net.Conn, hub *Hub) (*Client, error) {
	for {
		request, err := acceptAuthRequest(clientConn)
		if err != nil {
			return nil, err
		}

		response, client := hub.TryToAuthenticate(request)
		if response == ResponseOk {
			return client, client.forwardResponseToUser(ID(""), ResponseOk)
		}

		// try to communicate that we're retrying
		err = forwardResponseToUser(clientConn, ID(""), response)
		if err != nil {
			log.Printf("Error with %s: %s\n", client.Creds.Name, err)
			return nil, err
		}
	}
}

func forwardResponseToUser(conn net.Conn, id ID, r Response) error {
	_, err := conn.Write([]byte(ServerResponsePrefix + string(id) +
		IdSeparator + string(r) + "\n"))
	return err
}
func (client *Client) forwardResponseToUser(id ID, r Response) error {
	return forwardResponseToUser(client.conn, id, r)
}

func (client *Client) handleMessagesLoop() error {
	userInput := ReadAsyncIntoChan(bufio.NewScanner(client.conn))

	for {
		select {
		case input := <-userInput:
			if input.Err != nil {
				return input.Err
			}
			go func() {
				err := client.dispatchUserInput(input.Val)
				if err != nil {
					client.errs <- err
					return
				}
			}()
		case err := <-client.errs:
			return err
		case msg := <-client.pendingMsgs:
			go func() {
				err := client.forwardMsg(msg)
				msg.Ack()
				if err != nil {
					client.errs <- err
					return
				}
			}()
		}
	}
}


func isCommand(s string) bool {
	return strings.HasPrefix(s, cmdPrefix)
}

func parseInputMsg(input string) (id ID, msg string, ok bool) {
	if !strings.HasPrefix(input, MsgPrefix) {
		return "", "", false
	}
	input = input[len(MsgPrefix):]
	parts := strings.Split(input, IdSeparator)
	if len(parts) < 2 {
		return "", "", false
	}
	id = ID(parts[0])
	msg = input[len(id)+len(IdSeparator):]
	return id, msg, true
}

func (client *Client) dispatchUserInput(input string) error {
	if id, msg, ok := parseInputMsg(input); ok {
		if isCommand(msg) {
			cmd := ToCmd(msg)
			err := client.forwardResponseToUser(id, ResponseOk)
			if err != nil {
				return err
			}
			return client.runUserCommand(cmd)
		} else {
			response := client.hub.BroadcastMessageWait(msg, client.Creds)
			return client.forwardResponseToUser(id, response)
		}
	} else {
		return ErrOddOutput
	}
}

func (client *Client) runUserCommand(cmd Cmd) error {
	switch cmd {
	case LogoutCmd:
		client.hub.Logout(client.Creds)
		return client.forwardCmd(LogoutCmd)
	default:
		msg := NewChatMessage(&UserCredentials{Name: "runServer"}, "Invalid command")
		return client.forwardMsg(msg)
	}
}

func (client *Client) forwardMsg(msg ChatMessage) error {
	_, err := client.conn.Write([]byte(MsgPrefix + msg.sender.Name + ": " +
		string(msg.content) + "\n"))
	return err
}

const cmdPrefix = "/"

func (client *Client) forwardCmd(cmd Cmd) error {
	_, err := client.conn.Write([]byte(cmdPrefix + string(cmd) + "\n"))
	return err
}
