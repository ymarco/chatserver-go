package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
)

type AuthAction string
const (
	ActionLogin AuthAction = "l"
	ActionRegister AuthAction = "r"
	ActionIOErr AuthAction = ""
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

func handleClient(clientConn net.Conn) {
	defer closePrintErr(clientConn)
	defer log.Printf("Disconnected: %s\n", clientConn.RemoteAddr())
	client, receiveMsg, err := acceptAuthRetry(clientConn)
	if err == ErrClientHasQuit {
		return
	} else if err != nil {
		log.Println(err)
		return
	}
	defer logout(client)
	if err := sendResponse(ResponseOk, clientConn); err != nil {
		log.Printf("Error with %s: %s\n", client.name, err)
	}

	handleMessagesLoop(clientConn, client, receiveMsg)
}

func acceptAuthRetry(clientConn net.Conn) (*UserCredentials, <-chan ChatMessage, error) {
	for {
		client, action, err := acceptAuthRequest(clientConn)
		if err != nil {
			return nil, nil, err
		}

		send, receive := NewMessagePipe()
		response := tryToAuthenticate(action, client, send)
		if response == ResponseOk {
			return client, receive, nil
		}

		// try to communicate that we're retrying
		err = sendResponse(response, clientConn)
		if err != nil {
			log.Printf("Error with %s: %s\n", client.name, err)
			return nil, nil, err
		}
	}
}

func sendResponse(r Response, clientConn net.Conn) error {
	_, err := clientConn.Write([]byte(string(r) + "\n"))
	return err
}

func handleMessagesLoop(clientConn net.Conn, client *UserCredentials, receiveMsg <-chan ChatMessage) {
	clientInput := readAsyncIntoChan(bufio.NewScanner(clientConn))

	for {
		select {
		case input := <-clientInput:
			if input.err == ErrClientHasQuit {
				return
			} else if input.err != nil {
				log.Println(input.err)
				return
			}
			err := dispatchClientInput(input.val, client, clientConn)
			if err != nil {
				log.Println(input.err)
				return
			}
		case msg := <-receiveMsg:
			err := passMessageToClient(clientConn, msg)
			msg.Ack()
			if err != nil {
				log.Println(err)
				return
			}
		}
	}
}

func isCommand(s string) bool {
	return strings.HasPrefix(s, "/");
}
func dispatchClientInput(input string, client *UserCredentials, clientConn net.Conn) error {
	if isCommand(input) {
		return runUserCommand(input[1:], client, clientConn)
	} else {
		response := broadcastMessageWait(input, client)
		return sendResponse(response, clientConn)
	}
}

const LogoutCmd = "$logout$"

func runUserCommand(cmd string, client *UserCredentials, clientConn net.Conn) error {
	err := sendResponse(ResponseOk, clientConn)
	if err != nil {
		return err
	}
	switch cmd {
	case "quit":
		logout(client)
		return passCommandToRunToClient(clientConn, LogoutCmd)
	default:
		m := NewChatMessage(&UserCredentials{name: "server"}, "Invalid command")
		return passMessageToClient(clientConn, m)
	}
}

func passMessageToClient(writer io.Writer, msg ChatMessage) error {
	_, err := writer.Write([]byte(msg.sender.name + ": " + msg.content + "\n"))
	return err
}
func passCommandToRunToClient(writer io.Writer, cmd string) error {
	_, err := writer.Write([]byte(cmd + "\n"))
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
