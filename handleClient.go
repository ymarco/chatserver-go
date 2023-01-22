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

func acceptAuthRequest(clientConn net.Conn) (*UserCredentials, AuthAction, error) {
	clientOutput := bufio.NewScanner(clientConn)
	choice, err := scanLine(clientOutput)
	if err != nil {
		return nil, ActionIOErr, err
	}
	action, err := strToAuthAction(choice)
	if err != nil {
		return nil, ActionIOErr, err
	}

	username, err := scanLine(clientOutput)
	if err != nil {
		return nil, ActionIOErr, err
	}

	password, err := scanLine(clientOutput)
	if err != nil {
		return nil, ActionIOErr, err
	}

	return &UserCredentials{username, password}, action, nil
}

func handleClient(hub *Hub, clientConn net.Conn) {
	defer closePrintErr(clientConn)
	defer log.Printf("Disconnected: %s\n", clientConn.RemoteAddr())
	client := &Client{conn: clientConn, hub: hub}
	err := client.acceptAuthRetry()
	if err == ErrClientHasQuit {
		return
	} else if err != nil {
		log.Println(err)
		return
	}
	defer hub.Logout(client.creds)
	if err := client.sendResponse(ResponseOk); err != nil {
		log.Printf("Error with %s: %s\n", client.creds.name, err)
	}

	client.handleMessagesLoop()
}

func (client *Client) acceptAuthRetry() error {
	for {
		creds, action, err := acceptAuthRequest(client.conn)
		if err != nil {
			return err
		}
		client.creds = creds

		send, receive := NewMessagePipe()
		client.sendMsg = send
		client.receiveMsg = receive

		response := client.hub.TryToAuthenticate(action, client)
		if response == ResponseOk {
			return nil
		}

		// try to communicate that we're retrying
		err = client.sendResponse(response)
		if err != nil {
			log.Printf("Error with %s: %s\n", client.creds.name, err)
			return err
		}
	}
}

func (client *Client) sendResponse(r Response) error {
	_, err := client.conn.Write([]byte(string(r) + "\n"))
	return err
}

func (client *Client) handleMessagesLoop() {
	userInput := readAsyncIntoChan(bufio.NewScanner(client.conn))

	for {
		select {
		case input := <-userInput:
			if input.err == ErrClientHasQuit {
				return
			} else if input.err != nil {
				log.Println(input.err)
				return
			}
			err := client.dispatchUserInput(input.val)
			if err != nil {
				log.Println(input.err)
				return
			}
		case msg := <-client.receiveMsg:
			err := client.passMessageToUser(msg)
			msg.Ack()
			if err != nil {
				log.Println(err)
				return
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
		return client.sendResponse(response)
	}
}

type UserCommand string

const (
	LogoutCmd UserCommand = "/quit"
)

func (client *Client) runUserCommand(cmd string) error {
	err := client.sendResponse(ResponseOk)
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
		}
	}()
	return outputs
}
