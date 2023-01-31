package client

import (
	"bufio"
	"context"
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

	shouldReconnect := true
	for shouldReconnect {
		shouldReconnect = runClientUntilDisconnected(port, userInput, out)
	}
}

type UnauthenticatedClient struct {
	errs chan error

	receiveResponse <-chan ServerResponse
	receiveMsg      <-chan string
	serverInput     io.Writer

	pendingResponsesForMsgs map[MsgID]chan<- Response
	// a pointer to avoid copying when turning into an authenticated client
	pendingResponsesLock *sync.Mutex

	userInput  <-chan ReadInput
	userOutput io.Writer
}

type Client struct {
	UnauthenticatedClient
	creds *UserCredentials
	relog chan struct{}
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
) {
	scanner := bufio.NewScanner(output)
	responses := make(chan ServerResponse, 32870)
	msgs := make(chan string, 32870)
	go func() {
		defer close(responses)
		defer close(msgs)
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
			} else {
				fmt.Printf("odd output from server: %s\n", str)
			}
		}
	}()
	return responses, msgs
}

func startSession(port string, userInput <-chan ReadInput, out io.Writer) *UnauthenticatedClient {
	serverConn, err := connectToPortWithRetry(port, out)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("Connected to %s\n", serverConn.RemoteAddr())
	errs := make(chan error, 128)
	responses, msgs := splitServerOutputAsync(serverConn, errs)
	serverInput := serverConn.(io.Writer)
	pendingAcks := make(map[MsgID]chan<- Response)

	return &UnauthenticatedClient{errs, responses, msgs, serverInput, pendingAcks, &sync.Mutex{}, userInput, out}
}

func runClientUntilDisconnected(port string, userInput <-chan ReadInput, out io.Writer) (shouldReconnect bool) {
	log.SetOutput(out)
	unauthedClient := startSession(port, userInput, out)
	defer ClosePrintErr(unauthedClient.serverInput.(net.Conn))

	action := RetryActionShouldOnlyRelog
	for action == RetryActionShouldOnlyRelog {
		action = unauthedClient.runUntilLoggedOut()
	}

	return action == RetryActionShouldReconnect
}

type RetryAction int

const (
	RetryActionShouldOnlyRelog RetryAction = iota
	RetryActionShouldReconnect
	RetryActionShouldExit
)

func (unauthedClient *UnauthenticatedClient) runUntilLoggedOut() RetryAction {
	client, err := authenticateWithRetry(unauthedClient)
	if err != nil {
		if err == io.EOF {
			fmt.Fprintln(unauthedClient.userOutput, "Server closed, retrying")
			return RetryActionShouldOnlyRelog
		}
		log.Fatalln(err)
	}
	fmt.Fprintf(unauthedClient.userOutput, "Logged in as %s\n\n", client.creds.Name)
	defer log.Println("Logged out")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.handleResponsesLoop(ctx)
	go client.handleUserInputLoop(ctx)
	go client.receiveMsgsLoop(ctx)
	select {
	case <-client.relog:
		return RetryActionShouldOnlyRelog
	case err := <-client.errs:
		switch err {
		case nil:
			panic("unreachable, mainClientLoop should return only on error")
		case ErrUserHasQuit:
			return RetryActionShouldExit
		case io.EOF, ErrServerTimedOut, net.ErrClosed:
			log.Println("Server closed, retrying in 5 seconds")
			time.Sleep(5 * time.Second)
			return RetryActionShouldReconnect
		default:
			log.Println(err)
			return RetryActionShouldExit
		}
	}
}

func (client *Client) handleResponsesLoop(ctx context.Context) {
	for {
		select {
		case serverResponse, ok := <-client.receiveResponse:
			if !ok {
				return
			}
			client.handleIncomingResponse(serverResponse)
		case <-ctx.Done():
			return
		}
	}
}

var ErrResponseForUnexpectedId = errors.New("got a response for an id we didn't send")

func (client *Client) handleIncomingResponse(serverResponse ServerResponse) {
	client.pendingResponsesLock.Lock()
	defer client.pendingResponsesLock.Unlock()
	respond, exists := client.pendingResponsesForMsgs[serverResponse.Id]
	if !exists {
		fmt.Printf("id we didn't expect: id = %s\n", string(serverResponse.Id))
		client.errs <- ErrResponseForUnexpectedId
		return
	}
	respond <- serverResponse.Response
}

var ErrUserHasQuit = errors.New("client has quit")

func authenticateWithRetry(client *UnauthenticatedClient) (*Client, error) {
	for {
		creds, action, err := promptForAuthTypeAndUser(client.userInput, client.userOutput)
		if err != nil {
			if err == ErrClientHasQuit {
				return nil, ErrUserHasQuit
			}
			return nil, err
		}

		client, err := client.authenticateWithServer(creds, action)
		if err != ErrInvalidAuth {
			return client, err
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

func (client *Client) receiveMsgsLoop(ctx context.Context) {
	for {
		select {
		case msg, ok := <-client.receiveMsg:
			if !ok {
				return
			}
			fmt.Fprintln(client.userOutput, msg)
		case <-ctx.Done():
			return
		}
	}
}

func (client *Client) handleUserInputLoop(ctx context.Context) {
	for {
		select {
		case line, ok := <-client.userInput:
			if !ok {
				return
			}
			if line.Err != nil {
				if line.Err == io.EOF {
					client.errs <- ErrUserHasQuit
					return
				}
				client.errs <- line.Err
				return
			}
			if IsCmd(line.Val) {
				client.dispatchCmd(UnserializeStrToCmd(line.Val))
			} else {
				client.sendMsgExpectAsyncResponse(line.Val)
			}
		case <-ctx.Done():
			return
		}
	}
}

const QuitCmd Cmd = "quit"

func (client *Client) dispatchCmd(cmd Cmd) {
	switch cmd {
	case QuitCmd:
		err := client.sendMsgWithTimeout("", cmd.Serialize())
		if err != nil {
			client.errs <- err
		}
		// no waiting for response
		client.relog <- struct{}{}
	default:
		_, err := client.userOutput.Write([]byte("Unknown command"))
		if err != nil {
			client.errs <- err
			return
		}
	}
}

func (client *Client) sendMsgExpectAsyncResponse(msgContent string) {
	id := getUniqueID()

	ack := client.insertExpectedResponseId(id)
	err := client.sendMsgWithTimeout(id, msgContent)
	if err != nil {
		client.errs <- err
		return
	}
	go client.expectResponseFromChanWithTimeout(id, ack, ResponseOk)
}

var globalID int64 = 0

func getUniqueID() MsgID {
	new_ := atomic.AddInt64(&globalID, 1)
	return MsgID(strconv.FormatInt(new_, 10))
}

func (client *Client) insertExpectedResponseId(id MsgID) <-chan Response {
	ack := make(chan Response, 1)

	client.pendingResponsesLock.Lock()
	defer client.pendingResponsesLock.Unlock()

	client.pendingResponsesForMsgs[id] = ack
	return ack
}
func (client *Client) removeExpectedResponseId(id MsgID) {
	client.pendingResponsesLock.Lock()
	defer client.pendingResponsesLock.Unlock()
	delete(client.pendingResponsesForMsgs, id)
}

func (client *Client) expectResponseFromChanWithTimeout(id MsgID, ack <-chan Response, expected Response) {
	select {
	case <-time.After(MsgAckTimeout):
		log.Printf("Msg %s wasn't acked", id)
		// skip err, i.e don't send it to client.errs
	case response := <-ack:
		if response != expected {
			fmt.Printf("Response was unexpectedly %s\n", response)
		}
	}
	client.removeExpectedResponseId(id)
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

func promptForAuthTypeAndUser(userInput <-chan ReadInput, out io.Writer) (*UserCredentials, AuthAction, error) {
	action, err := ChooseLoginOrRegister(userInput, out)
	if err != nil {
		return nil, action, err
	}

	creds, err := promptForUsernameAndPassword(userInput, out)
	return creds, action, nil
}

var ErrInvalidAuth = errors.New("username exists and such")

func (unauthedClient *UnauthenticatedClient) authenticateWithServer(creds *UserCredentials, action AuthAction) (*Client, error) {
	err, response := unauthedClient.authenticate(action, creds)
	if err != nil {
		return nil, err
	}
	if response != ResponseOk {
		fmt.Fprintln(unauthedClient.userOutput, response)
		return nil, ErrInvalidAuth
	}
	client := &Client{*unauthedClient, creds, make(chan struct{})}
	return client, nil
}

func ChooseLoginOrRegister(userInput <-chan ReadInput, out io.Writer) (AuthAction, error) {
	for {
		fmt.Fprintln(out, "Type "+ActionRegister+" to register, "+ActionLogin+" to login")

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

func promptForUsernameAndPassword(userInput <-chan ReadInput, out io.Writer) (*UserCredentials, error) {
	fmt.Fprintf(out, "Username:\n")

	inputtedUsername := <-userInput
	if inputtedUsername.Err != nil {
		return nil, inputtedUsername.Err
	}
	if inputtedUsername.Val == "" {
		return nil, ErrEmptyUsernameOrPassword
	}

	fmt.Fprintf(out, "Password:\n")
	inputtedPassword := <-userInput
	if inputtedPassword.Err != nil {
		return nil, inputtedPassword.Err
	}
	if inputtedPassword.Val == "" {
		return nil, ErrEmptyUsernameOrPassword
	}
	return &UserCredentials{Name: Username(inputtedUsername.Val),
		Password: Password(inputtedPassword.Val)}, nil
}

func (unauthedClient *UnauthenticatedClient) authenticate(action AuthAction, creds *UserCredentials) (error, Response) {
	_, err := unauthedClient.serverInput.Write([]byte(
		string(action) + "\n" +
			string(creds.Name) + "\n" +
			string(creds.Password) + "\n"))
	if err != nil {
		return err, ResponseIoErrorOccurred
	}

	var response Response
	select {
	case serverResponse := <-unauthedClient.receiveResponse:
		response = serverResponse.Response
	case err := <-unauthedClient.errs:
		return err, ResponseIoErrorOccurred
	}
	// ignore serverResponse.id since we didn't send an id (and there's only one msg
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
