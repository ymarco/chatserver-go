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

func acceptLogin(clientConn net.Conn, hub UserHub) (User, LoginAction, error) {
	clientOutput := bufio.NewScanner(clientConn)
	clientOutput.Scan()
	err := clientOutput.Err()
	if err != nil {
		return User{}, ActionRegister, err
	}
	action, err := strToLoginAction(clientOutput.Text())
	if err != nil {
		return User{}, ActionRegister, err
	}

	clientOutput.Scan()
	err = clientOutput.Err()
	if err != nil {
		return User{}, ActionRegister, err
	}
	username := clientOutput.Text()
	clientOutput.Scan()
	err = clientOutput.Err()
	if err != nil {
		return User{}, ActionRegister, err
	}
	password := clientOutput.Text()
	return User{username, password}, action, nil

}

func handleClient(clientConn net.Conn, hub UserHub) {
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
	if code == ReturnOk {
		confirmLoggedIn(clientConn)
	} else {
		sendLoginError(code, clientConn)
		goto retry
	}
	defer hub.Logout(client)

	MainClientLoop(clientConn, hub, client, control)
}

func sendLoginError(code ReturnCode, clientConn net.Conn) {
	switch code {
	case ReturnUserAlreadyOnline:
		clientConn.Write([]byte("online"))
	case ReturnUsernameExists:
		clientConn.Write([]byte("exists"))
	}
}

func confirmLoggedIn(clientConn net.Conn) {
	clientConn.Write([]byte("success\n"))
}

func MainClientLoop(clientConn net.Conn, hub UserHub, client User, control UserControl) {
	userInput, err := readAsyncIntoChan(clientConn)
loop:
	for {
		select {
		case err_ := <-err:
			if err_ == io.EOF {
				// client disconnected
				hub.Logout(client)
				break loop
			} else {
				log.Println(err_)
			}
			log.Println("handleClient main loop: quitting")
			break loop
		case line := <-userInput:
			hub.SendMessage(line, client)
		case <-control.quit:
			break loop
		case msg := <-control.messages:
			printMsg(clientConn, msg)
			msg.Ack()
		}
	}
}

func printMsg(writer io.Writer, msg ChatMessage) {
	writer.Write([]byte(msg.user.name))
	writer.Write([]byte(": "))
	writer.Write([]byte(msg.content))
}

// Read lines from reader and send them to outputs.
func readAsyncIntoChan(reader io.Reader) (outputs chan string, err chan error) {
	outputs = make(chan string)
	err = make(chan error)
	scanner := bufio.NewScanner(reader)
	go func() {
		for {
			scanner.Scan()
			err_ := scanner.Err()
			if err_ != nil {
				fmt.Printf("ReadAsync error %s\n", err_)
				err <- err_
				return
			}
			fmt.Printf("ReadAsync read '%s'\n", scanner.Text())
			outputs <- scanner.Text()
		}
	}()
	return outputs, err
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
