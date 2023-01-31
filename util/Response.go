package util

import (
	"errors"
	"strings"
)

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

var ErrOddOutput = errors.New("unexpected output from server")
var ResponseUnknown Response = "unexpected output from server"
