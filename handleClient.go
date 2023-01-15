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
	if code != ReturnOk {
		if err := sendLoginError(code, clientConn); err != nil {
			log.Printf("Error with %s: %s\n", client.name, err)
			return
		}
		goto retry
	}
	defer hub.Logout(client)
	if err := confirmLoggedIn(clientConn); err != nil {
		log.Printf("Error with %s: %s\n", client.name, err)
	}

	MainClientLoop(clientConn, hub, client, control)
}

func sendLoginError(code ReturnCode, clientConn net.Conn) error {
	switch code {
	case ReturnUserAlreadyOnline:
		if _, err := clientConn.Write([]byte("online")); err != nil {
			return err
		}
	case ReturnUsernameExists:
		if _, err := clientConn.Write([]byte("exists")); err != nil {
			return err
		}
	}
	return nil
}

func confirmLoggedIn(clientConn net.Conn) error {
	_, err := clientConn.Write([]byte("success\n"))
	return err
}

func MainClientLoop(clientConn net.Conn, hub UserHub, client User, control UserControl) {
	userInput, errChan := readAsyncIntoChan(clientConn)
loop:
	for {
		select {
		case err := <-errChan:
			if err == io.EOF {
				// client disconnected
				hub.Logout(client)
				break loop
			} else {
				log.Println(err)
			}
			log.Printf("handleClient main loop for %s: quitting\n", client.name)
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

func readAsyncIntoChan(reader io.Reader) (outputs chan string, errChan chan error) {
	outputs = make(chan string)
	errChan = make(chan error)
	scanner := bufio.NewScanner(reader)
	go func() {
		for {
			scanner.Scan()
			err_ := scanner.Err()
			if err_ != nil {
				fmt.Printf("ReadAsync error %s\n", err_)
				errChan <- err_
				return
			}
			fmt.Printf("ReadAsync read '%s'\n", scanner.Text())
			outputs <- scanner.Text()
		}
	}()
	return outputs, errChan
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
