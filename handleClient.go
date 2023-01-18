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

func strToLoginAction(s string) (AuthAction, error) {
	switch s {
	case "r":
		return ActionRegister, nil
	case "l":
		return ActionLogin, nil
	case "": // happens when a user quits without choosing
		return ActionRegister, io.EOF
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

func acceptLogin(clientConn net.Conn) (User, AuthAction, error) {
	clientOutput := bufio.NewScanner(clientConn)
	choice, err := scanLine(clientOutput)
	if err != nil {
		return User{}, ActionRegister, err
	}
	action, err := strToLoginAction(choice)
	if err != nil {
		return User{}, ActionRegister, err
	}

	username, err := scanLine(clientOutput)
	if err != nil {
		return User{}, ActionRegister, err
	}

	password, err := scanLine(clientOutput)
	if err != nil {
		return User{}, ActionRegister, err
	}

	return User{username, password}, action, nil
}

func handleClient(clientConn net.Conn) {
	defer closePrintErr(clientConn)
	defer log.Printf("Disconnected: %s\n", clientConn.RemoteAddr())
retry:
	client, action, err := acceptLogin(clientConn)
	if err == io.EOF {
		return
	} else if err != nil {
		log.Printf("Error: %s", err)
		return
	}

	controller := NewUserController()
	response := tryToLogin(action, client, controller)
	if response != ResponseOk {
		if err := sendReturnCode(response, clientConn); err != nil {
			log.Printf("Error with %s: %s\n", client.name, err)
			return
		}
		goto retry
	}
	log.Printf("Logged in: %s\n", client.name)
	defer logout(client)
	if err := sendReturnCode(ResponseOk, clientConn); err != nil {
		log.Printf("Error with %s: %s\n", client.name, err)
	}

	mainHandleClientLoop(clientConn, client, controller)
}

func sendReturnCode(code Response, clientConn net.Conn) error {
	_, err := clientConn.Write([]byte(string(code) + "\n"))
	return err
}

func mainHandleClientLoop(clientConn net.Conn, client User, controller UserController) {
	clientInput := readAsyncIntoChan(bufio.NewScanner(clientConn))

	for {
		select {
		case input := <-clientInput:
			if input.err == io.EOF {
				// client disconnected
				return
			} else if input.err != nil {
				log.Println(input.err)
				return
			}
			if strings.HasPrefix(input.val, "/") {
				runUserCommand(input.val[1:], client, clientConn)
				continue
			}
			err := sendReturnCode(broadcastMessageWait(input.val, client), clientConn)
			if err != nil {
				log.Println(input.err)
				return
			}
		case <-controller.quit:
			fmt.Println("quit")
			return
		case msg := <-controller.messages:
			printMsg(clientConn, msg)
			msg.Ack()
		}
	}
}

func runUserCommand(s string, client User, clientConn net.Conn) {
	sendReturnCode(ResponseOk, clientConn)
	switch s {
	case "quit":
		logout(client)
		printCmd(clientConn, "logout")
	default:
		m := NewChatMessage(User{name: "server"}, "Invalid command")
		printMsg(clientConn, m)
	}
}

func printMsg(writer io.Writer, msg ChatMessage) error {
	_, err := writer.Write([]byte(msg.user.name + ": " + msg.content + "\n"))
	return err
}
func printCmd(writer io.Writer, cmd string) error {
	_, err := writer.Write([]byte(cmd + "\n"))
	return err
}

type ReadOutput struct {
	val string
	err error
}

func readAsyncIntoChan(scanner *bufio.Scanner) (outputs chan ReadOutput) {
	outputs = make(chan ReadOutput)
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
