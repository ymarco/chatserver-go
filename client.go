package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Msg string

type Cmd string

func isCmd(s string) bool {
	return strings.HasPrefix(s, "/")
}
func toCmd(s string) Cmd {
	return Cmd(s[1:])
}

func client(port string, in io.Reader, out io.Writer) {
	go generateIDs()
	userInput := readAsyncIntoChan(bufio.NewScanner(in))

	shouldRetry := true
	for shouldRetry {
		shouldRetry = runClientUntilDisconnected(port, userInput, out)
	}
}

type ServerResponse struct {
	response Response
	id       ID
}

type UnauthenticatedClient struct {
	errs chan error

	serverResponses <-chan ServerResponse
	serverMsgs      <-chan string
	serverCmds      <-chan Cmd
	serverInput     io.Writer

	pendingAcks     map[ID]chan<- Response
	pendingAcksLock *sync.Mutex // a pointer to avoid copying when turning
	// into an authenticated client

	userInput  <-chan ReadOutput
	userOutput io.Writer
}

var getUniqueID = make(chan ID)

func intToID(x int) ID {
	return ID(strconv.Itoa(x))
}


func generateIDs() {
	i := 0
	for {
		getUniqueID <- intToID(i)
		i++
	}
}

type AuthenticatedClient struct {
	UnauthenticatedClient
	creds *UserCredentials
}

func parseIncomingMsg(s string) (msg string, ok bool) {
	if !strings.HasPrefix(s, msgPrefix) {
		return "", false
	}
	s = s[len(msgPrefix):]
	return s, true
}

func splitServerOutputAsync(output io.Reader, errs chan<- error) (
	responses_ <-chan ServerResponse,
	msgs_ <-chan string,
	cmds_ <-chan Cmd,
) {
	scanner := bufio.NewScanner(output)
	responses := make(chan ServerResponse, 128)
	msgs := make(chan string, 128)
	cmds := make(chan Cmd, 128)
	go func() {
		defer close(responses)
		defer close(msgs)
		defer close(cmds)
		for {
			s, err := scanLine(scanner)
			if err != nil {
				errs <- err
				return
			}
			if serverResponse, ok := parseServerResponse(s); ok {
				responses <- serverResponse
			} else if msg, ok := parseIncomingMsg(s); ok {
				msgs <- msg
			} else if isCmd(s) {
				cmds <- toCmd(s)
			}

		}
	}()
	return responses, msgs, cmds
}

func connectToSocket(port string, userInput <-chan ReadOutput, out io.Writer) *UnauthenticatedClient {
	serverConn, err := connectToPortWithRetry(port, out)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("Connected to %s\n", serverConn.RemoteAddr())
	errs := make(chan error, 128)
	responses, msgs, cmds := splitServerOutputAsync(serverConn, errs)
	serverInput := serverConn.(io.Writer)
	pendingAcks := make(map[ID]chan<- Response)

	return &UnauthenticatedClient{errs, responses, msgs, cmds,
		serverInput, pendingAcks, &sync.Mutex{}, userInput, out}
}

func runClientUntilDisconnected(port string, userInput <-chan ReadOutput, out io.Writer) (shouldRetry bool) {
	log.SetOutput(out)
	client := connectToSocket(port, userInput, out)
	defer closePrintErr(client.serverInput.(net.Conn))
	me, err := authenticateWithRetry(client)
	if err == io.EOF {
		fmt.Fprintln(out, "Server closed, retrying")
		return true
	} else if err != nil {
		log.Fatalln(err)
	}
	fmt.Fprintf(out, "Logged in as %s\n\n", me.creds.name)

	go me.handleResponsesLoop() // should return once we close serverInput since
	// read would produce an error
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
		log.Fatalln(err)
	}

	return false // unreachable
}

func (client *AuthenticatedClient) handleResponsesLoop() {
	for sResponse := range client.serverResponses {
		client.handleIncomingResponse(sResponse)
	}
}

var ErrResponseForUnexpectedId = errors.New("got a response for an id we didn't send")

func (client *AuthenticatedClient) handleIncomingResponse(sResponse ServerResponse) {
	client.pendingAcksLock.Lock()
	defer client.pendingAcksLock.Unlock()
	if ack, exists := client.pendingAcks[sResponse.id]; exists {
		delete(client.pendingAcks, sResponse.id)
		ack <- sResponse.response
	} else {
		fmt.Printf("id we didn't expect: id = %s, while map is ", string(sResponse.id))
		for k := range client.pendingAcks {
			fmt.Print(k, " ")
		}
		fmt.Println()
		client.errs <- ErrResponseForUnexpectedId
	}
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
		case err := <-client.errs:
			return err
		case cmd := <-client.serverCmds:
			go client.runCmd(cmd)
		case msg := <-client.serverMsgs:
			fmt.Fprintln(client.userOutput, msg)
		case line := <-client.userInput:
			if line.err == ErrClientHasQuit {
				return ErrClientHasQuitExtinguished
			}
			if line.err != nil {
				return line.err
			}
			go func() {
				id := <-getUniqueID
				go client.expectResponseForIdWithTimeout(id, ResponseOk)
				err := client.sendMsgWithTimeout(id, line.val)
				if err != nil {
					client.errs <- err
					return
				}
			}()

		}
	}
}

var ErrMsgWasntAcked = errors.New("didn't get an ack for msg")

func (client *AuthenticatedClient) insertExpectedResponseId(id ID) <-chan Response {
	ack := make(chan Response, 1)

	client.pendingAcksLock.Lock()
	defer client.pendingAcksLock.Unlock()

	client.pendingAcks[id] = ack
	return ack
}
func (client *AuthenticatedClient) expectResponseForIdWithTimeout(id ID, expected Response) {
	ack := client.insertExpectedResponseId(id)
	select {
	case <-time.After(MsgAckTimeout):
		client.errs <- ErrMsgWasntAcked
	case response := <-ack:
		if response != expected {
			fmt.Printf("Response for ID %s was %s\n", id, response)
		}
	}
}

var ErrServerLoggedUsOut = errors.New("server logged us out")
var ErrUnknownCommand = errors.New("unknown command")

func (client *AuthenticatedClient) runCmd(cmd Cmd) {
	switch cmd {
	case LogoutCmd:
		client.errs <- ErrServerLoggedUsOut
	default:
		client.errs <- ErrUnknownCommand
	}
}

var ErrInvalidCast = errors.New("couldn't cast")

func (client *AuthenticatedClient) sendMsgWithTimeout(id ID, msg string) error {
	conn, ok := client.serverInput.(net.Conn)
	if !ok {
		return ErrInvalidCast
	}
	conn.SetWriteDeadline(time.Now().Add(MsgSendTimeout))
	_, err := conn.Write([]byte(msgPrefix + string(id) + idSeparator + msg + "\n"))
	conn.SetWriteDeadline(time.Time{})
	return err
}

var ErrServerTimedOut = errors.New("server timed out")

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

	var response Response
	select {
	case sResponse := <-client.serverResponses:
		response = sResponse.response
	case err := <-client.errs:
		return err, ResponseIoErrorOccurred
	}
	// ignore sResponse.id since we didn't send an id (and there's only one msg
	// the server could be responding to)

	if response == ResponseOk ||
		response == ResponseUserAlreadyOnline ||
		response == ResponseUsernameExists ||
		response == ResponseInvalidCredentials {
		return nil, response
	}
	log.Println(response)
	return ErrOddOutput, ResponseUnknown
}
