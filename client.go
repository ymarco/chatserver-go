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
	shouldRetry := true
	for shouldRetry {
		shouldRetry = runClientUntilDisconnected(port, in, out)
	}
}

func runClientUntilDisconnected(port string, in io.Reader, out io.Writer) (shouldRetry bool) {
	log.SetOutput(out)
	serverConn, err := connectToPortWithRetry(port, out)
	if err != nil {
		log.Fatalln(err)
	}
	defer closePrintErr(serverConn)
	log.Printf("Connected to %s\n", serverConn.RemoteAddr())

	userInput := bufio.NewScanner(in)
	me, err := authenticateWithRetry(userInput, out, serverConn)
	if err == io.EOF {
		fmt.Fprintln(out, "Server closed, retrying")
		return true
	} else if err != nil {
		log.Fatalln(err)
	}
	fmt.Fprintf(out, "Logged in as %s\n\n", me.name)

	err = handleClientMessagesLoop(userInput, out, serverConn)
	switch err {
	case nil:
		panic("unreachable, mainClientLoop should return only on error")
	case ErrServerLoggedUsOut:
		log.Println("Logged out and disconnected. Reconnecting...")
		return true
	case io.EOF, ErrServerTimedOut, net.ErrClosed:
		log.Println(out, "Server closed, retrying in 5 seconds")
		time.Sleep(5 * time.Second)
		return true
	default:
		log.Fatalln(out, err)
	}

	return false // unreachable
}

func authenticateWithRetry(userInput *bufio.Scanner, out io.Writer, serverConn net.Conn) (*User, error) {
	for {
		me, action, err := promptForAuthTypeAndUser(userInput, out)
		if err != nil {
			return nil, err
		}

		err = authenticateWithServer(out, me, action, serverConn)
		if err != ErrInvalidAuth {
			return me, err
		}
	}

}
func errIsConnectionRefused(err error) bool {
	if oerr, ok := err.(*net.OpError); ok {
		if serr, ok := oerr.Err.(*os.SyscallError); ok && serr.Err == syscall.ECONNREFUSED {
			return true
		}
	}
	return false
}
func connectToPortWithRetry(port string, out io.Writer) (net.Conn, error) {
	for {
		serverConn, err := net.Dial("tcp4", port)

		if err != nil {
			if errIsConnectionRefused(err) {
				log.SetOutput(out)
				log.Println("Connection refused, retrying in 5 seconds")
				time.Sleep(5 * time.Second)
				continue
			}

			return nil, err
		}

		return serverConn, nil
	}
}

var ErrServerLoggedUsOut = errors.New("server logged us out")

func handleClientMessagesLoop(userInput_ *bufio.Scanner, out io.Writer, serverConn net.Conn) error {
	serverOutput := readAsyncIntoChan(bufio.NewScanner(serverConn))
	userInput := readAsyncIntoChan(userInput_)
	for {
		select {
		case msg := <-serverOutput:
			if msg.err != nil {
				return msg.err
			}
			if msg.val == LogoutCmd {
				return ErrServerLoggedUsOut
			} else {
				fmt.Fprintln(out, msg.val)
			}
		case line := <-userInput:
			if line.err != nil {
				return line.err
			}
			if err := sendMsgWithTimeout(line.val, serverConn); err != nil {
				return err
			}
			if err := expectOkWithTimeout(serverOutput); err != nil {
				return err
			}
		}
	}
}

func sendMsgWithTimeout(msg string, serverConn net.Conn) error {
	finished := make(chan error)
	go func() {
		_, err := serverConn.Write([]byte(msg + "\n"))
		finished <- err
	}()

	select {
	case err := <-finished:
		return err
	case <-time.After(100 * time.Millisecond):
		return ErrServerTimedOut
	}
}

var ErrServerTimedOut = errors.New("server timed out")

func expectOkWithTimeout(serverOutput <-chan ReadOutput) error {
	select {
	case ack := <-serverOutput:
		if ack.err != nil {
			return ack.err
		}
		if Response(ack.val) != ResponseOk {
			return fmt.Errorf("Message sending error: %s\n", ack.val)
		}
		return nil
	case <-time.After(5 * time.Second):
		return ErrServerTimedOut
	}
}

func promptForAuthTypeAndUser(userInput *bufio.Scanner, out io.Writer) (*User, AuthAction, error) {
	action, err := ChooseLoginOrRegister(userInput, out)
	if err != nil {
		return nil, action, err
	}

	me, err := promptForUsernameAndPassword(userInput, out)
	return me, action, nil
}

var ErrInvalidAuth = errors.New("username exists and such")

func authenticateWithServer(out io.Writer, client *User, action AuthAction,
	serverConn io.ReadWriter) error {
	err, response := authenticate(action, client, serverConn)
	if err != nil {
		return err
	}
	if response != ResponseOk {
		fmt.Fprintln(out, response)
		return ErrInvalidAuth
	}
	return nil
}

func ChooseLoginOrRegister(userInput *bufio.Scanner, out io.Writer) (AuthAction, error) {
	for {
		fmt.Fprintln(out, "Type r to register, l to login")

		c, err := scanLine(userInput)
		if err != nil {
			return ActionIOErr, err
		}
		a := AuthAction(c)
		switch a {
		case ActionLogin, ActionRegister:
			return a, nil
		default:
			continue
		}
	}
}

var ErrEmptyUsernameOrPassword = errors.New("empty username or password")

func promptForUsernameAndPassword(userInput *bufio.Scanner, out io.Writer) (*User, error) {
	fmt.Fprintf(out, "Username:\n")

	username, err := scanLine(userInput)
	if err != nil {
		return nil, err
	}
	if username == "" {
		return nil, ErrEmptyUsernameOrPassword
	}

	fmt.Fprintf(out, "Password:\n")
	password, err := scanLine(userInput)
	if err != nil {
		return nil, err
	}
	if password == "" {
		return nil, ErrEmptyUsernameOrPassword
	}
	return &User{username, password}, nil
}

var ErrOddOutput = errors.New("weird output from server")

func authenticate(action AuthAction, user *User, serverConn io.ReadWriter) (error, Response) {
	_, err := serverConn.Write([]byte(
		string(action) + "\n" +
			user.name + "\n" +
			user.password + "\n"))
	if err != nil {
		return err, ResponseIoErrorOccurred
	}

	serverOutput := bufio.NewScanner(serverConn)
	status, err := scanLine(serverOutput)

	if err != nil {
		return err, ResponseIoErrorOccurred
	}

	switch Response(status) {
	case ResponseOk:
		return nil, ResponseOk
	case ResponseUserAlreadyOnline:
		return nil, ResponseUserAlreadyOnline
	case ResponseUsernameExists:
		return nil, ResponseUsernameExists
	case ResponseInvalidCredentials:
		return nil, ResponseInvalidCredentials

	default:
		log.Println(status)
		return ErrOddOutput, ResponseOk
	}
}
