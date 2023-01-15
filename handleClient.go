package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
)

type LoginAction int

const (
	ActionLogin LoginAction = iota
	ActionRegister
)

func strToLoginAction(s string) (LoginAction, error) {
	switch s {
	case "r":
		return ActionRegister, nil
	case "l":
		return ActionLogin, nil
	case "": // happens when a user quits without choosing
		return ActionRegister, io.EOF
	default:
		return ActionRegister, fmt.Errorf("Weird output from clientConn: %s", s)
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

func acceptLogin(clientConn net.Conn, hub UserHub) (User, LoginAction, error) {
	clientOutput := bufio.NewScanner(clientConn)
	choise, err := scanLine(clientOutput)
	if err != nil {
		return User{}, ActionRegister, err
	}
	action, err := strToLoginAction(choise)
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

func handleClient(clientConn net.Conn, hub UserHub) {
	defer clientConn.Close()
retry:
	client, action, err := acceptLogin(clientConn, hub)
	if err == io.EOF {
		return
	} else if err != nil {
		log.Printf("Error: %s", err)
		return
	}

	control := NewUserControl()
	code := hub.Login(client, action, control)
	if code != ReturnOk {
		if err := sendLoginError(code, clientConn); err != nil {
			log.Printf("Error with %s: %s\n", client.name, err)
			return
		}
		goto retry
	}
	defer hub.Logout(client)
	if err := confirmSuccess(clientConn); err != nil {
		log.Printf("Error with %s: %s\n", client.name, err)
	}

	mainHandleClientLoop(clientConn, hub, client, control)
}

func sendLoginError(code ReturnCode, clientConn net.Conn) error {
	word := "\n"

	switch code {
	case ReturnUserAlreadyOnline:
		word = "online\n"
	case ReturnUsernameExists:
		word = "exists\n"
	case ReturnInvalidCredentials:
		word = "invalidCredentials\n"
	default:
		panic("unreachable")
	}
	if _, err := clientConn.Write([]byte(word)); err != nil {
		return err
	}
	return nil
}

func confirmSuccess(clientConn net.Conn) error {
	_, err := clientConn.Write([]byte("success\n"))
	return err
}

func mainHandleClientLoop(clientConn net.Conn, hub UserHub, client User, control UserControl) {
	clientInput := readAsyncIntoChan(bufio.NewScanner(clientConn))
loop:
	for {
		select {
		case input := <-clientInput:
			if input.err == io.EOF {
				// client disconnected
				break loop
			} else if input.err != nil {
				log.Println(input.err)
				break loop
			}
			hub.SendMessage(input.val, client)
			confirmSuccess(clientConn)
		case <-control.quit:
			fmt.Println("quit")
			break loop
		case msg := <-control.messages:
			printMsg(clientConn, msg)
			msg.Ack()
		}
	}
}

func printMsg(writer io.Writer, msg ChatMessage) error {
	if _, err := writer.Write([]byte(msg.user.name)); err != nil {
		return err
	}
	if _, err := writer.Write([]byte(": ")); err != nil {
		return err
	}
	if _, err := writer.Write([]byte(msg.content)); err != nil {
		return err
	}
	if _, err := writer.Write([]byte("\n")); err != nil {
		return err
	}
	return nil
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

func promptUsernameAndPassword(clientConn net.Conn) (User, error) {
	r := bufio.NewReader(clientConn)
	clientConn.Write([]byte("Username: "))
	name, err := r.ReadString('\n')

	if err != nil {
		return User{}, err
	}
	name = name[0 : len(name)-1]
	clientConn.Write([]byte("Password: "))
	pass, err := r.ReadString('\n')
	if err != nil {
		return User{}, err
	}
	pass = pass[0 : len(pass)-1]

	return User{name, pass}, nil
}
