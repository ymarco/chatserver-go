package main

import (
	"bufio"
	"client"
	"fmt"
	"io"
	"server"
	"testing"
	"time"
	. "util"
)

func TestStress(t *testing.T) {
	port := ":7000"
	go server.RunServer(port)
	time.Sleep(time.Millisecond * 100)
	client1 := NewClientRun(port)
	defer client1.Close()
	// client1.peek(t)
	client2 := NewClientRun(port)
	defer client2.Close()
	client1.RegisterWait(&server.UserCredentials{Name: "yoav", Password: "1234"}, t)
	client2.RegisterWait(&server.UserCredentials{Name: "bob", Password: "0987"}, t)

	// nMessages := 2 << 14
	// go spamMessages(client1.input, nMessages, t)
	// msgs := receiveMessages(client2.output, nMessages, t)
	// if msgs[len(msgs)-1] != client1.user.name+": Hello!" {
	// 	t.Error("IDK")
	// }
}

type ClientRoutineController struct {
	user   *server.UserCredentials
	input  *io.PipeWriter
	output *io.PipeReader
}

func NewClientRun(port string) (c ClientRoutineController) {
	stdin, clientIn := io.Pipe()
	c.input = clientIn
	clientOut, stdout := io.Pipe()
	c.output = clientOut
	go client.RunClient(port, stdin, stdout)
	return c
}
func (client *ClientRoutineController) peek(t *testing.T) {
	originalIn := client.input
	newStdin, newInInterface := io.Pipe()
	client.input = newInInterface

	go func() {
		s := bufio.NewScanner(newStdin)
		i, err := server.ScanLine(s)
		for err != nil {
			t.Logf("%s received: %s", client.user, i)
			originalIn.Write([]byte(i))
			i, err = server.ScanLine(s)
		}
	}()

	originalOut := client.output
	newOutInterface, newStdout := io.Pipe()
	client.output = newOutInterface

	go func() {
		s := bufio.NewScanner(originalOut)
		i, err := server.ScanLine(s)
		for err != nil {
			t.Logf("%s printed: %s", client.user, i)
			newStdout.Write([]byte(i))
			i, err = server.ScanLine(s)
		}
	}()
}

func (client *ClientRoutineController) Close() {
	ClosePrintErr(client.output)
	ClosePrintErr(client.input)
}
func (client *ClientRoutineController) RegisterWait(user *server.UserCredentials, t *testing.T) {
	client.user = user
	clientOut := bufio.NewScanner(client.output)
	fmt.Println("skipping line")
	if err := skipLine(clientOut); err != nil { // Connected as ...
		t.Error(err)
	}
	expect(clientOut, "Type r to register, l to login", t)
	_, err := client.input.Write([]byte("r\n"))
	if err != nil {
		t.Error(err)
	}
	expect(clientOut, "Username:", t)
	_, err = client.input.Write([]byte(client.user.Name + "\n"))
	if err != nil {
		t.Error(err)
	}
	expect(clientOut, "Password:", t)
	_, err = client.input.Write([]byte(client.user.Password + "\n"))
	if err != nil {
		t.Error(err)
	}
	expect(clientOut, "Logged in as "+client.user.Name, t)
	expect(clientOut, "", t)
}

func spamMessages(clientIn io.Writer, n int, t *testing.T) {
	for i := 0; i <= n; i++ {
		_, err := clientIn.Write([]byte("Hello!\n"))
		if err != nil {
			t.Error(err)
		}
		step := 1
		if i/n == (step*n)/10 {
			fmt.Printf("Sent %d%% of the messages\n", step)
			step++
		}
	}
}
func receiveMessages(clientOut io.Reader, n int, t *testing.T) []string {
	scanner := bufio.NewScanner(clientOut)
	res := make([]string, n)
	for i := 0; i < n; i++ {
		temp, err := server.ScanLine(scanner)
		res[i] = temp
		if err != nil {
			t.Error(err)
		}
	}
	return res
}
func skipLine(s *bufio.Scanner) error {
	_, err := server.ScanLine(s)
	return err
}
func expect(clientOut *bufio.Scanner, expected string, t *testing.T) {
	s, err := server.ScanLine(clientOut)
	if err != nil {
		t.Error("expect ", err)
	}
	if s != expected {
		t.Error(ErrOddOutput, s)
	}

}
func tookTooLong(fn func(), timeout time.Duration) bool {
	end := make(chan struct{})
	go func() {
		fn()
		end <- struct{}{}
	}()
	select {
	case <-end:
		return false
	case <-time.After(timeout):
		return true
	}
}
