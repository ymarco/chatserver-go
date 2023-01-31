package util

import "strings"

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

const (
	LogoutCmd Cmd = "quit"
)
