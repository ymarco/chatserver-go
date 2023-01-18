package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"syscall"
	"time"
)

func client(port string, in io.Reader, out io.Writer) {
	log.SetOutput(out)
	me := User{"", ""}
	action := ActionRegister
	haveUser := false
reconnect:
	serverConn, err := connectToPortWithRetry(port, out)
	defer closePrintErr(serverConn)
	log.Printf("Connected to %s\n", serverConn.RemoteAddr())
	userInput := bufio.NewScanner(in)
retryLogin:
	if !haveUser {
		me, action, err = promptForLoginTypeAndUser(userInput, out)
		if err == io.EOF {
			log.Println(out, "Server closed, retrying in 5 seconds")
			goto reconnect
		} else if err == ErrEmptyUsernameOrPassword {
			goto retryLogin
		} else if err != nil {
			log.Fatalln(err)
		}
	}
	err = LoginToServer(out, me, action, serverConn)
	if err == ErrLogin {
		goto retryLogin
	}
	if err == io.EOF {
		fmt.Fprintln(out, "Server closed, retrying in 5 seconds")
		goto reconnect
	} else if err != nil {
		log.Fatalln(err)
	}
	fmt.Fprintf(out, "Logged in as %s\n\n", me.name)
	err = mainClientLoop(me, userInput, out, serverConn)
	switch err {
	case nil:
		panic("unreachable, mainClientLoop should return only on error")
	case io.EOF:
		fallthrough
	case ErrServerTimedOut:
		fallthrough
	case net.ErrClosed:
		log.Println(out, "Server closed, retrying in 5 seconds")
		time.Sleep(5 * time.Second)
		goto reconnect
	case ErrServerQuit:
		haveUser = false
		goto reconnect
	default:
		fmt.Fprintln(out, err)
	}
}

func connectToPortWithRetry(port string, out io.Writer) (net.Conn, error) {
reconnect2:
	serverConn, err := net.Dial("tcp4", port)
	if oerr, ok := err.(*net.OpError); ok {
		if serr, ok := oerr.Err.(*os.SyscallError); ok && serr.Err == syscall.ECONNREFUSED {
			log.Println("Connection refused, retrying in 5 seconds")
			time.Sleep(5 * time.Second)
			goto reconnect2
		}
	}
	if err != nil {
		log.Fatalln(err)
	}
	return serverConn, err
}

var ErrSelfQuit = errors.New("our client has quit")
var ErrServerQuit = errors.New("server logged us out")

func mainClientLoop(me User, userInput_ *bufio.Scanner, out io.Writer, serverConn net.Conn) error {
	serverOutput := readAsyncIntoChan(bufio.NewScanner(serverConn))
	userInput := readAsyncIntoChan(userInput_)
	for {
		select {
		case msg := <-serverOutput:
			if msg.err != nil {
				return msg.err
			}
			if msg.val == "logout" {
				return ErrServerQuit
			} else {
				fmt.Fprintln(out, msg.val)
			}
		case line := <-userInput:
			if line.err == io.EOF {
				return ErrSelfQuit
			} else if line.err != nil {
				return line.err
			}
			if err := sendMessage(line.val, serverConn); err != nil {
				return err
			}
			if err := expectOk(serverOutput); err != nil {
				return err
			}
		}
	}
}

func sendMessage(msg string, serverConn net.Conn) error {
	_, err := serverConn.Write([]byte(msg + "\n"))
	if err != nil {
		return err
	}
	return nil
}

var ErrServerTimedOut = errors.New("server timed out")

func expectOk(serverOutput chan ReadOutput) error {
	select {
	case ack := <-serverOutput:
		if ack.err != nil {
			return ack.err
		}
		if ack.val != "Ok" {
			return fmt.Errorf("Message sending error: %s\n", ack.val)
		}
		return nil
	case <-time.After(5 * time.Second):
		return ErrServerTimedOut
	}

}

func promptForLoginTypeAndUser(userInput *bufio.Scanner, out io.Writer) (User, LoginAction, error) {
	action, err := ChooseLoginOrRegister(userInput, out)
	if err != nil {
		return User{}, action, err
	}

	me, err := promptForUsernameAndPassword(userInput, out)
	return me, action, nil
}

var ErrLogin = errors.New("Username exists and such")

func LoginToServer(out io.Writer, user User, action LoginAction,
	serverConn io.ReadWriter) error {
	err := login(action, user, serverConn)
	switch err {
	case nil:
		// all good
	case ErrUserOnline:
		fmt.Fprintln(out, "User already online")
		return ErrLogin
	case ErrUsernameExists:
		fmt.Fprintln(out, "Username exists")
		return ErrLogin
	case ErrInvalidCredentials:
		fmt.Fprintln(out, "Wrong username or password, try again")
		return ErrLogin
	default:
		return err
	}
	return nil
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

var ErrEmptyUsernameOrPassword = errors.New("empty username or password")

func promptForUsernameAndPassword(userInput *bufio.Scanner, out io.Writer) (User, error) {
	fmt.Fprintf(out, "Username:\n")
	username, err := scanLine(userInput)
	if err != nil {
		return User{}, err
	}
	if username == "" {
		return User{}, ErrEmptyUsernameOrPassword
	}

	fmt.Fprintf(out, "Password:\n")
	password, err := scanLine(userInput)
	if err != nil {
		return User{}, err
	}
	if password == "" {
		return User{}, ErrEmptyUsernameOrPassword
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

	serverConn.Write([]byte(user.name + "\n"))
	serverConn.Write([]byte(user.password + "\n"))

	serverOutput := bufio.NewScanner(serverConn)
	status, err := scanLine(serverOutput)
	if err != nil {
		return err
	}
	switch status {
	case "Ok":
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
