package client

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
	"sync/atomic"
	"syscall"
	"time"
	. "util"
)

func RunClient(port string, in io.Reader, out io.Writer) {
	userInput := ReadAsyncIntoChan(bufio.NewScanner(in))

	shouldRetry := true
	for shouldRetry {
		shouldRetry = runClientUntilDisconnected(port, userInput, out)
	}
}

type UnauthenticatedClient struct {
	errs chan error

	receiveResponse <-chan ServerResponse
	receiveMsg      <-chan string
	receiveCmd      <-chan Cmd
	serverInput     io.Writer

	pendingAcks     map[MsgID]chan<- Response
	pendingAcksLock *sync.Mutex // a pointer to avoid copying when turning
	// into an authenticated client

	userInput  <-chan ReadOutput
	userOutput io.Writer
}

type Client struct {
	UnauthenticatedClient
	creds *UserCredentials
}

func parseIncomingMsg(s string) (msg string, ok bool) {
	if !strings.HasPrefix(s, MsgPrefix) {
		return "", false
	}
	s = s[len(MsgPrefix):]
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
			str, err := ScanLine(scanner)
			if err != nil {
				errs <- err
				return
			}
			if serverResponse, ok := ParseServerResponse(str); ok {
				responses <- serverResponse
			} else if msg, ok := parseIncomingMsg(str); ok {
				msgs <- msg
			} else if IsCmd(str) {
				cmds <- ToCmd(str)
			} else {
				fmt.Printf("odd output from server: %s\n", str)
			}
		}
	}()
	return responses, msgs, cmds
}

func startSession(port string, userInput <-chan ReadOutput, out io.Writer) *UnauthenticatedClient {
	serverConn, err := connectToPortWithRetry(port, out)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("Connected to %s\n", serverConn.RemoteAddr())
	errs := make(chan error, 128)
	responses, msgs, cmds := splitServerOutputAsync(serverConn, errs)
	serverInput := serverConn.(io.Writer)
	pendingAcks := make(map[MsgID]chan<- Response)

	return &UnauthenticatedClient{errs, responses, msgs, cmds,
		serverInput, pendingAcks, &sync.Mutex{}, userInput, out}
}

func runClientUntilDisconnected(port string, userInput <-chan ReadOutput, out io.Writer) (shouldRetry bool) {
	log.SetOutput(out)
	unauthedClient := startSession(port, userInput, out)
	defer ClosePrintErr(unauthedClient.serverInput.(net.Conn))
	me, err := authenticateWithRetry(unauthedClient)
	if err != nil {
		if err == io.EOF {
			fmt.Fprintln(out, "Server closed, retrying")
			return true
		}
		log.Fatalln(err)
	}
	fmt.Fprintf(out, "Logged in as %s\n\n", me.creds.Name)

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

func (client *Client) handleResponsesLoop() {
	for sResponse := range client.receiveResponse {
		client.handleIncomingResponse(sResponse)
	}
}

var ErrResponseForUnexpectedId = errors.New("got a response for an id we didn't send")

func (client *Client) handleIncomingResponse(serverResponse ServerResponse) {
	client.pendingAcksLock.Lock()
	defer client.pendingAcksLock.Unlock()
	ack, exists := client.pendingAcks[serverResponse.Id]
	if !exists {
		fmt.Printf("id we didn't expect: id = %s\n", string(serverResponse.Id))
		client.errs <- ErrResponseForUnexpectedId
		return
	}
	delete(client.pendingAcks, serverResponse.Id)
	ack <- serverResponse.Response
}

var ErrClientHasQuitExtinguished = errors.New("client has quit")

func authenticateWithRetry(client *UnauthenticatedClient) (*Client, error) {
	for {
		creds, action, err := promptForAuthTypeAndUser(client.userInput, client.userOutput)
		if err != nil {
			if err == ErrClientHasQuit {
				return nil, ErrClientHasQuitExtinguished
			}
			return nil, err
		}

		me, err := client.authenticateWithServer(creds, action)
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

func (client *Client) handleClientMessagesLoop() error {
	for {
		select {
		case err := <-client.errs:
			return err
		case cmd := <-client.receiveCmd:
			go client.runCmd(cmd)
		case msg := <-client.receiveMsg:
			fmt.Fprintln(client.userOutput, msg)
		case line := <-client.userInput:
			if line.Err != nil {
				if line.Err == ErrClientHasQuit {
					return ErrClientHasQuitExtinguished
				}
				return line.Err
			}
			go client.sendMsgExpectResponseTimeout(line.Val)
		}
	}
}
func (client *Client) sendMsgExpectResponseTimeout(msgContent string) {
	id := getUniqueID()

	ack := client.insertExpectedResponseId(id)
	err := client.sendMsgWithTimeout(id, msgContent)
	if err != nil {
		client.errs <- err
		return
	}
	expectResponseFromChanWithTimeout(id, ack, ResponseOk)
}

var globalID int64 = 0

func getUniqueID() MsgID {
	new_ := atomic.AddInt64(&globalID, 1)
	return MsgID(strconv.FormatInt(new_, 10))
}

func (client *Client) insertExpectedResponseId(id MsgID) <-chan Response {
	ack := make(chan Response, 1)

	client.pendingAcksLock.Lock()
	defer client.pendingAcksLock.Unlock()

	client.pendingAcks[id] = ack
	return ack
}
func expectResponseFromChanWithTimeout(id MsgID, ack <-chan Response, expected Response) {
	select {
	case <-time.After(MsgAckTimeout):
		log.Printf("Msg %s wasn't acked", id)
		// skip err, i.e don't send it to client.errs
	case response := <-ack:
		if response != expected {
			fmt.Printf("Response was unexpectedly %s\n", response)
		}
	}
}

func (client *Client) runCmd(cmd Cmd) {
	switch cmd {
	case LogoutCmd:
		client.errs <- ErrServerLoggedUsOut
	default:
		log.Printf("Unknown command from server: %s", cmd)
		// skip err, i.e don't send it to client.errs
	}
}

var ErrInvalidCast = errors.New("couldn't cast")

func (client *Client) sendMsgWithTimeout(id MsgID, msg string) error {
	conn, ok := client.serverInput.(net.Conn)
	if !ok {
		return ErrInvalidCast
	}
	err := conn.SetWriteDeadline(time.Now().Add(MsgSendTimeout))
	if err != nil {
		return err
	}
	_, err = conn.Write([]byte(MsgPrefix + string(id) + IdSeparator + msg + "\n"))
	if err != nil {
		return err
	}
	err = conn.SetWriteDeadline(time.Time{})
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

func (client *UnauthenticatedClient) authenticateWithServer(creds *UserCredentials, action AuthAction) (*Client, error) {
	err, response := client.authenticate(action, creds)
	if err != nil {
		return nil, err
	}
	if response != ResponseOk {
		fmt.Fprintln(client.userOutput, response)
		return nil, ErrInvalidAuth
	}
	me := &Client{*client, creds}
	return me, nil
}

func ChooseLoginOrRegister(userInput <-chan ReadOutput, out io.Writer) (AuthAction, error) {
	for {
		fmt.Fprintln(out, "Type r to register, l to login")

		answer := <-userInput
		if answer.Err != nil {
			return ActionIOErr, answer.Err
		}
		action := AuthAction(answer.Val)
		switch action {
		case ActionLogin, ActionRegister:
			return action, nil
		}
	}
}

var ErrEmptyUsernameOrPassword = errors.New("empty username or password")

func promptForUsernameAndPassword(userInput <-chan ReadOutput, out io.Writer) (*UserCredentials, error) {
	fmt.Fprintf(out, "Username:\n")

	username := <-userInput
	if username.Err != nil {
		return nil, username.Err
	}
	if username.Val == "" {
		return nil, ErrEmptyUsernameOrPassword
	}

	fmt.Fprintf(out, "Password:\n")
	password := <-userInput
	if password.Err != nil {
		return nil, password.Err
	}
	if password.Val == "" {
		return nil, ErrEmptyUsernameOrPassword
	}
	return &UserCredentials{Name: username.Val, Password: password.Val}, nil
}

func (client *UnauthenticatedClient) authenticate(action AuthAction, creds *UserCredentials) (error, Response) {
	_, err := client.serverInput.Write([]byte(
		string(action) + "\n" +
			creds.Name + "\n" +
			creds.Password + "\n"))
	if err != nil {
		return err, ResponseIoErrorOccurred
	}

	var response Response
	select {
	case sResponse := <-client.receiveResponse:
		response = sResponse.Response
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
