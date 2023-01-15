package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
)

func client(port string) {
	serverConn, err := net.Dial("tcp4", port)
	if err != nil {
		log.Fatalln(err)
	}
	defer serverConn.Close()
	fmt.Println("Connected successfully")
	userInput := bufio.NewScanner(os.Stdin)
	me, err := loginLoopUntilSuccess(userInput, serverConn)
	if err != nil {
		log.Fatalln(err)
	}
	fmt.Printf("Connected as %s\n\n", me.name)
	err = mainClientLoop(me, userInput, serverConn)
	switch err {
	case nil:
		return
	case io.EOF:
		fallthrough
	case net.ErrClosed:
		fmt.Println("Server closed")
	default:
		fmt.Println(err)
	}

}

var ErrSelfQuit = errors.New("Our client has quit")

func mainClientLoop(me User, userInput_ *bufio.Scanner, serverConn net.Conn) error {
	serverOutput := readAsyncIntoChan(bufio.NewScanner(serverConn))
	userInput := readAsyncIntoChan(userInput_)
	for {
		select {
		case msg := <-serverOutput:
			if msg.err != nil {
				return msg.err
			}
			fmt.Println(msg.val)
		case line := <-userInput:
			if line.err == io.EOF {
				return ErrSelfQuit
			} else if line.err != nil {
				return line.err
			}
			if err := sendMessage(line.val, serverConn); err != nil {
				return err
			}
			if err := expectSuccess(serverOutput); err != nil {
				return err
			}
		}
	}
}

func sendMessage(msg string, serverConn net.Conn) error {
	_, err := serverConn.Write([]byte(msg))
	if err != nil {
		return err
	}
	_, err = serverConn.Write([]byte("\n"))
	if err != nil {
		return err
	}
	return nil
}

func expectSuccess(serverOutput chan ReadOutput) error {
	ack := <-serverOutput
	if ack.err != nil {
		return ack.err
	}
	if ack.val != "success" {
		return ErrOddServerOutput
	}
	return nil

}
func loginLoopUntilSuccess(userInput *bufio.Scanner, serverConn io.ReadWriter) (User, error) {
retry:
	action, err := ChooseLoginOrRegister(userInput)
	if err != nil {
		return User{}, err
	}

	me, err := promptForUsernameAndPassword(userInput)
	if err != nil {
		return User{}, err
	}
	err = login(action, me, serverConn)
	switch err {
	case nil:
		// all good
	case ErrUserOnline:
		fmt.Println("User already online")
		goto retry
	case ErrUsernameExists:
		fmt.Println("User already online")
		goto retry
	case ErrInvalidCredentials:
		fmt.Println("Wrong username or password, try again")
		goto retry
	default:
		return User{}, err
	}
	return me, nil
}

func ChooseLoginOrRegister(userInput *bufio.Scanner) (LoginAction, error) {
	for {
		fmt.Println("Type r to register, l to login")

		c, err := scanLine(userInput)
		if err != nil {
			return ActionRegister, err
		}
		switch c {
		case "r":
			return ActionRegister, nil
		case "l":
			return ActionLogin, nil
		default:
			continue
		}
	}
}

func promptForUsernameAndPassword(userInput *bufio.Scanner) (User, error) {
	fmt.Printf("Username: ")
	username, err := scanLine(userInput)
	if err != nil {
		return User{}, err
	}

	fmt.Printf("Password: ")
	password, err := scanLine(userInput)
	if err != nil {
		return User{}, err
	}

	return User{username, password}, nil
}

var ErrUsernameExists = errors.New("username already exists")
var ErrUserOnline = errors.New("username already online")
var ErrInvalidCredentials = errors.New("username already online")
var ErrOddServerOutput = errors.New("weird output from server")

func login(action LoginAction, user User, serverConn io.ReadWriter) error {
	switch action {
	case ActionLogin:
		serverConn.Write([]byte("l\n"))
	case ActionRegister:
		serverConn.Write([]byte("r\n"))
	}

	serverConn.Write([]byte(user.name))
	serverConn.Write([]byte("\n"))
	serverConn.Write([]byte(user.password))
	serverConn.Write([]byte("\n"))

	serverOutput := bufio.NewScanner(serverConn)
	status, err := scanLine(serverOutput)
	if err != nil {
		return err
	}
	switch status {
	case "success":
		return nil
	case "exists":
		return ErrUsernameExists
	case "online":
		return ErrUserOnline
	case "invalidCredentials":
		return ErrInvalidCredentials
	default:
		return ErrOddServerOutput
	}
}
