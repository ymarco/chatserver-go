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
	case "": // happens when a user quits without choosing
		return ActionRegister, ErrClientHasQuit
	default:
		return ActionRegister, fmt.Errorf("weird output from clientConn: %s", s)
	}
}

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

func acceptAuth(clientConn net.Conn) (*User, AuthAction, error) {
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
retry:
	client, action, err := acceptAuth(clientConn)
	if err == ErrClientHasQuit {
		return
	} else if err != nil {
		log.Printf("Error: %s", err)
		return
	}

	controller := NewUserController()
	response := tryToAuthenticate(action, client, controller)
	if response != ResponseOk {
		if err := sendResponse(response, clientConn); err != nil {
			log.Printf("Error with %s: %s\n", client.name, err)
			return
		}
		goto retry
	}
	defer logout(client)
	if err := sendResponse(ResponseOk, clientConn); err != nil {
		log.Printf("Error with %s: %s\n", client.name, err)
	}

	mainHandleClientLoop(clientConn, client, controller)
}

func sendResponse(r Response, clientConn net.Conn) error {
	_, err := clientConn.Write([]byte(string(r) + "\n"))
	return err
}

func mainHandleClientLoop(clientConn net.Conn, client *User, controller UserController) {
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
			var err error = nil
			if strings.HasPrefix(input.val, "/") {
				err = runUserCommand(input.val[1:], client, clientConn)
			} else {
				err = sendResponse(broadcastMessageWait(input.val, client), clientConn)
			}
			if err != nil {
				log.Println(input.err)
				return
			}
		case <-controller.quit:
			fmt.Println("quit")
			return
		case msg := <-controller.writeMessageToClient:
			passMessageToClient(clientConn, msg)
			msg.Ack()
		}
	}
}

const LogoutCmd = "$logout$"
func runUserCommand(s string, client *User, clientConn net.Conn) error {
	err := sendResponse(ResponseOk, clientConn)
	if err != nil {
		return err
	}
	switch s {
	case "quit":
		logout(client)
		return passCommandToRunToClient(clientConn, LogoutCmd)
	default:
		m := NewChatMessage(&User{name: "server"}, "Invalid command")
		return passMessageToClient(clientConn, m)
	}
}

func passMessageToClient(writer io.Writer, msg ChatMessage) error {
	_, err := writer.Write([]byte(msg.user.name + ": " + msg.content + "\n"))
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

func readAsyncIntoChan(scanner *bufio.Scanner) (<-chan ReadOutput) {
	outputs := make(chan ReadOutput)
	go func() {
		for {
			s, err := scanLine(scanner)
			outputs <- ReadOutput{s, err}
		}
	}()
	return outputs
}

func writeAsyncFromChan(writer io.Writer) (inputs chan string) {
	inputs = make(chan string)
	bufWriter := bufio.NewWriter(writer)
	go func() {
		for s := range inputs {
			bufWriter.WriteString(s)
		}
	}()
	return inputs
}
