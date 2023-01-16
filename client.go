package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

func client(port string, in io.Reader, out io.Writer) {
	serverConn, err := net.Dial("tcp4", port)
	if err != nil {
		log.Fatalln(err)
	}
	defer serverConn.Close()
	fmt.Fprintln(out, "Connected successfully")
	userInput := bufio.NewScanner(in)
	me, err := loginLoopUntilSuccess(userInput, out, serverConn)
	if err != nil {
		log.Fatalln(err)
	}
	fmt.Fprintf(out, "Connected as %s\n\n", me.name)
	err = mainClientLoop(me, userInput, out, serverConn)
	switch err {
	case nil:
		return
	case io.EOF:
		fallthrough
	case net.ErrClosed:
		fmt.Fprintln(out, "Server closed")
	default:
		fmt.Fprintln(out, err)
	}

}

var ErrSelfQuit = errors.New("Our client has quit")

func mainClientLoop(me User, userInput_ *bufio.Scanner, out io.Writer, serverConn net.Conn) error {
	serverOutput := readAsyncIntoChan(bufio.NewScanner(serverConn))
	userInput := readAsyncIntoChan(userInput_)
	for {
		select {
		case msg := <-serverOutput:
			if msg.err != nil {
				return msg.err
			}
			fmt.Fprintln(out, msg.val)
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
var ErrServerTimedOut = errors.New("server timed out")

func expectSuccess(serverOutput chan ReadOutput) error {
	select {
	case ack := <-serverOutput:
		if ack.err != nil {
			return ack.err
		}
		if ack.val != "success" {
			return fmt.Errorf("Message sending error: %s\n", ack.val)
		}
		return nil
	case <-time.After(200 * time.Millisecond):
		return ErrServerTimedOut
	}

}
func loginLoopUntilSuccess(userInput *bufio.Scanner, out io.Writer, serverConn io.ReadWriter) (User, error) {
retry:
	action, err := ChooseLoginOrRegister(userInput, out)
	if err != nil {
		return User{}, err
	}

	me, err := promptForUsernameAndPassword(userInput, out)
	if err != nil {
		return User{}, err
	}
	err = login(action, me, serverConn)
	switch err {
	case nil:
		// all good
	case ErrUserOnline:
		fmt.Fprintln(out, "User already online")
		goto retry
	case ErrUsernameExists:
		fmt.Fprintln(out, "User already online")
		goto retry
	case ErrInvalidCredentials:
		fmt.Fprintln(out, "Wrong username or password, try again")
		goto retry
	default:
		return User{}, err
	}
	return me, nil
}

func ChooseLoginOrRegister(userInput *bufio.Scanner, out io.Writer) (LoginAction, error) {
	for {
		fmt.Fprintln(out, "Type r to register, l to login")

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

func promptForUsernameAndPassword(userInput *bufio.Scanner, out io.Writer) (User, error) {
	fmt.Fprintf(out, "Username: ")
	username, err := scanLine(userInput)
	if err != nil {
		return User{}, err
	}

	fmt.Fprintf(out, "Password: ")
	password, err := scanLine(userInput)
	if err != nil {
		return User{}, err
	}

	return User{username, password}, nil
}

var ErrUsernameExists = errors.New("username already exists")
var ErrUserOnline = errors.New("username already online")
var ErrInvalidCredentials = errors.New("username already online")
var ErrOddOutput = errors.New("weird output from server")

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
		return ErrOddOutput
	}
}
