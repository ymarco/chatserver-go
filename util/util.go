package util

import (
	"errors"
	"io"
	"log"
	"strings"
)

func ClosePrintErr(c io.Closer) {
	err := c.Close()
	if err != nil {
		log.Println(err)
	}
}


type Response string

var (
	ResponseOk                 = Response("Ok")
	ResponseUserAlreadyOnline  = Response("User already online")
	ResponseUsernameExists     = Response("Username already exists")
	ResponseInvalidCredentials = Response("Wrong username or password")
	ResponseMsgFailedForSome   = Response("Message failed to send to some users")
	ResponseMsgFailedForAll    = Response("Message failed to send to any users")
	// ResponseIoErrorOccurred should be returned along with a normal error type
	ResponseIoErrorOccurred = Response("IO error, couldn't get a response")
)

type ID string
type ServerResponse struct {
	Response Response
	Id       ID
}

type Cmd string

func IsCmd(s string) bool {
	return strings.HasPrefix(s, "/")
}
func ToCmd(s string) Cmd {
	return Cmd(s[1:])
}

var ErrServerLoggedUsOut = errors.New("server logged us out")
var ErrUnknownCommand = errors.New("unknown command")

var ErrOddOutput = errors.New("unexpected output from server")
var ResponseUnknown Response = "unexpected output from server"
