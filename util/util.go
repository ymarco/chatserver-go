package util

import (
	"bufio"
	"errors"
	"io"
	"log"
	"strings"
	"time"
)

func ClosePrintErr(c io.Closer) {
	err := c.Close()
	if err != nil {
		log.Println(err)
	}
}

type Response string

var (
	ResponseOk                 Response = "Ok"
	ResponseUserAlreadyOnline           = Response("User already online")
	ResponseUsernameExists              = Response("Username already exists")
	ResponseInvalidCredentials          = Response("Wrong username or password")
	ResponseMsgFailedForSome            = Response("Message failed to send to some users")
	ResponseMsgFailedForAll             = Response("Message failed to send to any users")
	// ResponseIoErrorOccurred should be returned along with a normal error type
	ResponseIoErrorOccurred = Response("IO error, couldn't get a response")
)

type MsgID string
type ServerResponse struct {
	Response Response
	Id       MsgID
}

const ServerResponsePrefix = "r"

func ParseServerResponse(s string) (ServerResponse, bool) {
	if !strings.HasPrefix(s, ServerResponsePrefix) {
		return ServerResponse{}, false
	}
	s = s[len(ServerResponsePrefix):]
	parts := strings.Split(s, IdSeparator)
	if len(parts) < 2 {
		return ServerResponse{}, false
	}
	id := MsgID(parts[0])
	response := Response(s[len(id)+len(IdSeparator):])
	return ServerResponse{Response: response, Id: id}, true
}

type Cmd string

const CmdPrefix = "/"

func IsCmd(s string) bool {
	return strings.HasPrefix(s, CmdPrefix)
}
func UnserializeStrToCmd(s string) Cmd {
	return Cmd(s[1:])
}
func (cmd Cmd) Serialize() string {
	return CmdPrefix + string(cmd)
}

var ErrServerLoggedUsOut = errors.New("server logged us out")

var ErrOddOutput = errors.New("unexpected output from server")
var ResponseUnknown Response = "unexpected output from server"

var ErrClientHasQuit = io.EOF

type ReadOutput struct {
	Val string
	Err error
}

func ReadAsyncIntoChan(scanner *bufio.Scanner) <-chan ReadOutput {
	outputs := make(chan ReadOutput)
	go func() {
		for {
			s, err := ScanLine(scanner)
			outputs <- ReadOutput{s, err}
			if err != nil {
				return
			}
		}
	}()
	return outputs
}

// ScanLine is a wrapper around Scanner.Scan() that returns EOF as errors
// instead of bools
func ScanLine(s *bufio.Scanner) (string, error) {
	if !s.Scan() {
		if s.Err() == nil {
			return "", io.EOF
		} else {
			return "", s.Err()
		}
	}
	return s.Text(), nil
}

type Username string
type Password string

type UserCredentials struct {
	Name     Username
	Password Password
}

const MsgPrefix = "m"
const IdSeparator = ";"

const MsgSendTimeout = time.Millisecond * 200
const MsgAckTimeout = time.Millisecond * 300

const (
	LogoutCmd Cmd = "quit"
)

type AuthAction string

const (
	ActionLogin    AuthAction = "l"
	ActionRegister AuthAction = "r"
	ActionIOErr    AuthAction = ""
)
