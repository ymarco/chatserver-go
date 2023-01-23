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
	receiveMsg <-chan ChatMessage
	sendMsg    chan<- ChatMessage
	creds      *UserCredentials
	conn       net.Conn
	hub        *Hub
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

func strToAuthAction(s string) (AuthAction, error) {
	a := AuthAction(s)
	switch a {
	case ActionRegister, ActionLogin:
		return a, nil
	case ActionIOErr: // happens when the client quits without choosing
		return ActionIOErr, ErrClientHasQuit
	default:
		return ActionIOErr, fmt.Errorf("weird output from clientConn: %s", s)
	}
}

func scanLine(s *bufio.Scanner) (string, error) {
	// a wrapper around Scanner.Scan() to return EOF as errors instead of bools
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
	return &Client{receiveMsg, sendMsg, r.creds, r.conn, hub}
}
func (client *Client) Close() error {
	close(client.sendMsg)
	return client.conn.Close()
}

func handleNewConnection(hub *Hub, conn net.Conn) {
	client := NewEmptyClient(conn, hub)
	defer closePrintErr(client)
	defer log.Printf("Disconnected: %s\n", conn.RemoteAddr())
	err := client.acceptAuthRetry()
	if err == ErrClientHasQuit {
		return
	} else if err != nil {
		log.Printf("Err with %s: %s", client.creds.name, err)
		return
	}
	defer hub.Logout(client.creds)

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
			return client, client.passResponseToUser(ResponseOk)
		}

		// try to communicate that we're retrying
		err = client.passResponseToUser(response)
		if err != nil {
			log.Printf("Error with %s: %s\n", client.creds.name, err)
			return nil, err
		}
	}
}

func (client *Client) passResponseToUser(r Response) error {
	_, err := client.conn.Write([]byte(string(r) + "\n"))
	return err
}

func (client *Client) handleMessagesLoop() error {
	userInput := readAsyncIntoChan(bufio.NewScanner(client.conn))

	for {
		select {
		case input := <-userInput:
			if input.err != nil {
				return input.err
			}
			err := client.dispatchUserInput(input.val)
			if err != nil {
				return err
			}
		case msg := <-client.receiveMsg:
			err := client.passMessageToUser(msg)
			msg.Ack()
			if err != nil {
				return err
			}
		}
	}
}

func isCommand(s string) bool {
	return strings.HasPrefix(s, "/")
}
func (client *Client) dispatchUserInput(input string) error {
	if isCommand(input) {
		return client.runUserCommand(input)
	} else {
		response := client.hub.broadcastMessageWait(input, client.creds)
		return client.passResponseToUser(response)
	}
}

type UserCommand string

const (
	LogoutCmd UserCommand = "/quit"
)

func (client *Client) runUserCommand(cmd string) error {
	err := client.passResponseToUser(ResponseOk)
	if err != nil {
		return err
	}
	switch cmd {
	case string(LogoutCmd):
		client.hub.Logout(client.creds)
		return client.passCommandToUser(LogoutCmd)
	default:
		m := NewChatMessage(&UserCredentials{name: "server"}, "Invalid command")
		return client.passMessageToUser(m)
	}
}

func (client *Client) passMessageToUser(msg ChatMessage) error {
	_, err := client.conn.Write([]byte(msg.sender.name + ": " + msg.content + "\n"))
	return err
}
func (client *Client) passCommandToUser(cmd UserCommand) error {
	_, err := client.conn.Write([]byte(string(cmd) + "\n"))
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
