package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
)

type AuthAction int

const (
	ActionLogin AuthAction = iota
	ActionRegister
)

var ErrClientHasQuit = io.EOF

func strToAuthAction(s string) (AuthAction, error) {
	switch s {
	case "r":
		return ActionRegister, nil
	case "l":
		return ActionLogin, nil
	case "": // happens when the client quits without choosing
		return ActionRegister, ErrClientHasQuit
	default:
		return ActionRegister, fmt.Errorf("weird output from clientConn: %s", s)
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

func acceptAuthRequest(clientConn net.Conn) (*User, AuthAction, error) {
	clientOutput := bufio.NewScanner(clientConn)
	choice, err := scanLine(clientOutput)
	if err != nil {
		return nil, ActionRegister, err
	}
	action, err := strToAuthAction(choice)
	if err != nil {
		return nil, ActionRegister, err
	}

	username, err := scanLine(clientOutput)
	if err != nil {
		return nil, ActionRegister, err
	}

	password, err := scanLine(clientOutput)
	if err != nil {
		return nil, ActionRegister, err
	}

	return &User{username, password}, action, nil
}

func handleClient(clientConn net.Conn) {
	defer closePrintErr(clientConn)
	defer log.Printf("Disconnected: %s\n", clientConn.RemoteAddr())
	client, receiveMsg, err := tryToAcceptAuthRetry(clientConn)
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

func tryToAcceptAuthRetry(clientConn net.Conn) (*User, <-chan ChatMessage, error) {
	for {
		client, action, err := acceptAuthRequest(clientConn)
		if err != nil {
			return nil, nil, err
		}
		send, receive := NewMessageChannel()
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

func handleMessagesLoop(clientConn net.Conn, client *User, receiveMsg <-chan ChatMessage) {
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

func dispatchClientInput(input string, client *User, clientConn net.Conn) error {
	if strings.HasPrefix(input, "/") {
		return runUserCommand(input[1:], client, clientConn)
	} else {
		response := broadcastMessageWait(input, client)
		return sendResponse(response, clientConn)
	}
}

const LogoutCmd = "$logout$"

func runUserCommand(cmd string, client *User, clientConn net.Conn) error {
	err := sendResponse(ResponseOk, clientConn)
	if err != nil {
		return err
	}
	switch cmd {
	case "quit":
		logout(client)
		return passCommandToRunToClient(clientConn, LogoutCmd)
	default:
		m := NewChatMessage(&User{name: "server"}, "Invalid command")
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
