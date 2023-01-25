package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
)

type UserCredentials struct {
	name     string
	password string
}
type Client struct {
	pendingMsgs <-chan ChatMessage
	SendMsg     chan<- ChatMessage
	errs        chan error
	Creds       *UserCredentials
	conn        net.Conn
	hub         *Hub
}

type AuthAction string

const (
	ActionLogin    AuthAction = "l"
	ActionRegister AuthAction = "r"
	ActionIOErr    AuthAction = ""
)

type AuthRequest struct {
	authType AuthAction
	conn     net.Conn
	creds    *UserCredentials
}

var ErrClientHasQuit = io.EOF

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

// scanLine is a wrapper around Scanner.Scan() to return EOF as errors instead
// of bools
func scanLine(s *bufio.Scanner) (string, error) {
	if !s.Scan() {
		if s.Err() == nil {
			return "", io.EOF
		} else {
			return "", s.Err()
		}
	}
	return s.Text(), nil
}

func acceptAuthRequest(clientConn net.Conn) (*AuthRequest, error) {
	clientOutput := bufio.NewScanner(clientConn)
	choice, err := scanLine(clientOutput)
	if err != nil {
		return nil, err
	}
	action, err := strToAuthAction(choice)
	if err != nil {
		return nil, err
	}

	username, err := scanLine(clientOutput)
	if err != nil {
		return nil, err
	}

	password, err := scanLine(clientOutput)
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

func (hub *Hub) handleNewConnection(conn net.Conn) {
	defer log.Printf("Disconnected: %s\n", conn.RemoteAddr())
	client, err := acceptAuthRetry(conn, hub)
	if err == ErrClientHasQuit {
		return
	} else if err != nil {
		log.Printf("Err with %s: %s", client.Creds.name, err)
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

		response, client := hub.tryToAuthenticate(request)
		if response == ResponseOk {
			return client, client.forwardResponse(ID(""), ResponseOk)
		}

		// try to communicate that we're retrying
		err = forwardResponse(clientConn, ID(""), response)
		if err != nil {
			log.Printf("Error with %s: %s\n", client.Creds.name, err)
			return nil, err
		}
	}
}

const serverResponsePrefix = "r"

func forwardResponse(conn net.Conn, id ID, r Response) error {
	_, err := conn.Write([]byte(serverResponsePrefix + string(id) +
		idSeparator + string(r) + "\n"))
	return err
}
func (client *Client) forwardResponse(id ID, r Response) error {
	return forwardResponse(client.conn, id, r)
}

func (client *Client) handleMessagesLoop() error {
	userInput := readAsyncIntoChan(bufio.NewScanner(client.conn))

	for {
		select {
		case input := <-userInput:
			if input.err != nil {
				return input.err
			}
			go func() {
				err := client.dispatchUserInput(input.val)
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

type ID string

func isCommand(s string) bool {
	return strings.HasPrefix(s, cmdPrefix)
}

func parseInputMsg(input string) (id ID, msg string, ok bool) {
	if !strings.HasPrefix(input, msgPrefix) {
		return "", "", false
	}
	input = input[len(msgPrefix):]
	parts := strings.Split(input, idSeparator)
	if len(parts) < 2 {
		return "", "", false
	}
	id = ID(parts[0])
	msg = input[len(id)+len(idSeparator):]
	return id, msg, true
}

func (client *Client) dispatchUserInput(input string) error {
	if id, msg, ok := parseInputMsg(input); ok {
		if isCommand(msg) {
			cmd := toCmd(msg)
			err := client.forwardResponse(id, ResponseOk)
			if err != nil {
				return err
			}
			return client.runUserCommand(cmd)
		} else {
			response := client.hub.BroadcastMessageWait(msg, client.Creds)
			return client.forwardResponse(id, response)
		}
	} else {
		return ErrOddOutput
	}
}

const (
	LogoutCmd Cmd = "quit"
)

func (client *Client) runUserCommand(cmd Cmd) error {
	switch cmd {
	case LogoutCmd:
		client.hub.Logout(client.Creds)
		return client.forwardCmd(LogoutCmd)
	default:
		msg := NewChatMessage(&UserCredentials{name: "server"}, "Invalid command")
		return client.forwardMsg(msg)
	}
}

const msgPrefix = "m"
const idSeparator = ";"

func (client *Client) forwardMsg(msg ChatMessage) error {
	_, err := client.conn.Write([]byte(msgPrefix + msg.sender.name + ": " +
		string(msg.content) + "\n"))
	return err
}

const cmdPrefix = "/"

func (client *Client) forwardCmd(cmd Cmd) error {
	_, err := client.conn.Write([]byte(cmdPrefix + string(cmd) + "\n"))
	return err
}

type ReadOutput struct {
	val string
	err error
}

func readAsyncIntoChan(scanner *bufio.Scanner) <-chan ReadOutput {
	outputs := make(chan ReadOutput)
	go func() {
		for {
			s, err := scanLine(scanner)
			outputs <- ReadOutput{s, err}
			if err != nil {
				return
			}
		}
	}()
	return outputs
}
