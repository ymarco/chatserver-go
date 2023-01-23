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
	userInput := readAsyncIntoChan(bufio.NewScanner(in))

	shouldRetry := true
	for shouldRetry {
		shouldRetry = runClientUntilDisconnected(port, userInput, out)
	}
}

type UnauthenticatedClient struct {
	serverOutput <-chan ReadOutput
	serverInput  io.Writer

	userInput  <-chan ReadOutput
	userOutput io.Writer
}
type AuthenticatedClient struct {
	UnauthenticatedClient
	creds *UserCredentials
}

func connectToSocket(port string, userInput <-chan ReadOutput, out io.Writer) *UnauthenticatedClient {
	serverConn, err := connectToPortWithRetry(port, out)
	if err != nil {
		log.Fatalln(err)
	}
	defer closePrintErr(serverConn)
	log.Printf("Connected to %s\n", serverConn.RemoteAddr())
	serverOutput := readAsyncIntoChan(bufio.NewScanner(serverConn))
	serverInput := serverConn.(io.Writer)

	return &UnauthenticatedClient{serverOutput, serverInput, userInput, out}
}
func runClientUntilDisconnected(port string, userInput <-chan ReadOutput, out io.Writer) (shouldRetry bool) {
	log.SetOutput(out)
	client := connectToSocket(port, userInput, out)
	me, err := authenticateWithRetry(client)
	if err == io.EOF {
		fmt.Fprintln(out, "Server closed, retrying")
		return true
	} else if err != nil {
		log.Fatalln(err)
	}
	fmt.Fprintf(out, "Logged in as %s\n\n", me.creds.name)

	err = me.handleClientMessagesLoop()
	switch err {
	case nil:
		panic("unreachable, mainClientLoop should return only on error")
	case ErrServerLoggedUsOut:
		log.Println("Logged out and disconnected. Reconnecting...")
		return true
	case ErrClientHasQuitExtinguished:
		return false
	case io.EOF, ErrServerTimedOut, net.ErrClosed:
		log.Println("Server closed, retrying in 5 seconds")
		time.Sleep(5 * time.Second)
		return true
	default:
		log.Fatalln(out, err)
	}

	return false // unreachable
}

var ErrClientHasQuitExtinguished = errors.New("client has quit")

func authenticateWithRetry(client *UnauthenticatedClient) (*AuthenticatedClient, error) {
	for {
		creds, action, err := promptForAuthTypeAndUser(client.userInput, client.userOutput)
		if err == ErrClientHasQuit {
			return nil, ErrClientHasQuitExtinguished
		}
		if err != nil {
			return nil, err
		}

		me, err := client.authenticateWithServer(creds, action)
		if err != ErrInvalidAuth {
			return me, err
		} else {
			continue
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

func (client *AuthenticatedClient) handleClientMessagesLoop() error {
	for {
		select {
		case msg := <-client.serverOutput:
			if msg.err != nil {
				return msg.err
			}
			if isCommand(msg.val) {
				err := runClientCommand(UserCommand(msg.val))
				if err != nil {
					return ErrServerLoggedUsOut
				}
			} else { // normal message
				fmt.Fprintln(client.userOutput, msg.val)
			}
		case line := <-client.userInput:
			if line.err == ErrClientHasQuit {
				return ErrClientHasQuitExtinguished
			}
			if line.err != nil {
				return line.err
			}
			if err := client.sendMsgWithTimeout(line.val); err != nil {
				return err
			}
			if err := expectResponseWithTimeout(client.serverOutput, ResponseOk); err != nil {
				return err
			}
		}
	}
}

var ErrServerLoggedUsOut = errors.New("server logged us out")
var ErrUnknownCommand = errors.New("unknown command")

func runClientCommand(cmd UserCommand) error {
	switch cmd {
	case LogoutCmd:
		return ErrServerLoggedUsOut
	default:
		return ErrUnknownCommand
	}
}

var ErrInvalidCast = errors.New("couldn't cast")

func (client *AuthenticatedClient) sendMsgWithTimeout(msg string) error {
	conn, ok := client.serverInput.(net.Conn)
	if !ok {
		return ErrInvalidCast
	}
	conn.SetWriteDeadline(time.Now().Add(MsgSendTimeout))
	_, err := conn.Write([]byte(msg + "\n"))
	conn.SetWriteDeadline(time.Time{})
	return err
}

var ErrServerTimedOut = errors.New("server timed out")

func expectResponseWithTimeout(serverOutput <-chan ReadOutput, r Response) error {
	select {
	case ack := <-serverOutput:
		if ack.err != nil {
			return ack.err
		}
		if Response(ack.val) != r {
			return fmt.Errorf("Message sending error: %s\n", ack.val)
		}
		return nil
	case <-time.After(5 * time.Second):
		return ErrServerTimedOut
	}
}

func promptForAuthTypeAndUser(userInput <-chan ReadOutput, out io.Writer) (*UserCredentials, AuthAction, error) {
	action, err := ChooseLoginOrRegister(userInput, out)
	if err != nil {
		return nil, action, err
	}

	me, err := promptForUsernameAndPassword(userInput, out)
	return me, action, nil
}

var ErrInvalidAuth = errors.New("username exists and such")

func (client *UnauthenticatedClient) authenticateWithServer(creds *UserCredentials, action AuthAction) (*AuthenticatedClient, error) {
	err, response := client.authenticate(action, creds)
	if err != nil {
		return nil, err
	}
	if response != ResponseOk {
		fmt.Fprintln(client.userOutput, response)
		return nil, ErrInvalidAuth
	}
	me := &AuthenticatedClient{*client, creds}
	return me, nil
}

func ChooseLoginOrRegister(userInput <-chan ReadOutput, out io.Writer) (AuthAction, error) {
	for {
		fmt.Fprintln(out, "Type r to register, l to login")

		answer := <-userInput
		if answer.err != nil {
			return ActionIOErr, answer.err
		}
		action := AuthAction(answer.val)
		switch action {
		case ActionLogin, ActionRegister:
			return action, nil
		default:
			continue
		}
	}
}

var ErrEmptyUsernameOrPassword = errors.New("empty username or password")

func promptForUsernameAndPassword(userInput <-chan ReadOutput, out io.Writer) (*UserCredentials, error) {
	fmt.Fprintf(out, "Username:\n")

	username := <-userInput
	if username.err != nil {
		return nil, username.err
	}
	if username.val == "" {
		return nil, ErrEmptyUsernameOrPassword
	}

	fmt.Fprintf(out, "Password:\n")
	password := <-userInput
	if password.err != nil {
		return nil, password.err
	}
	if password.val == "" {
		return nil, ErrEmptyUsernameOrPassword
	}
	return &UserCredentials{username.val, password.val}, nil
}

var ErrOddOutput = errors.New("unexpected output from server")
var ResponseUnknown Response = "unexpected output from server"

func (client *UnauthenticatedClient) authenticate(action AuthAction, creds *UserCredentials) (error, Response) {
	_, err := client.serverInput.Write([]byte(
		string(action) + "\n" +
			creds.name + "\n" +
			creds.password + "\n"))
	if err != nil {
		return err, ResponseIoErrorOccurred
	}

	status := <-client.serverOutput

	if status.err != nil {
		return err, ResponseIoErrorOccurred
	}

	response := Response(status.val)
	if response == ResponseOk ||
		response == ResponseUserAlreadyOnline ||
		response == ResponseUsernameExists ||
		response == ResponseInvalidCredentials {
		return nil, response
	}
	log.Println(status)
	return ErrOddOutput, ResponseUnknown
}
