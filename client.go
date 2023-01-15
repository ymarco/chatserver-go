package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
)

func client(port string) {
	serverConn, err := net.Dial("tcp4", port)
	if err != nil {
		log.Fatalln(err)
	}
	defer serverConn.Close()
	fmt.Println("Connected successfully")
	userInput := bufio.NewScanner(os.Stdin)
	me, err := loginLoopUntilSuccess(userInput, serverConn)
	if err != nil {
		log.Fatalln(err)
	}
	fmt.Printf("Connected as %s\n\n", me.name)
}
func loginLoopUntilSuccess(userInput *bufio.Scanner, serverConn io.ReadWriter) (User, error) {
retry:
	action, err := ChooseLoginOrRegister(userInput)
	if err != nil {
		return User{}, err
	}

	me, err := promptForUsernameAndPassword(userInput)
	if err != nil {
		return User{}, err
	}
	err = login(action, me, serverConn)
	switch err {
	case nil:
		// all good
	case ErrUserOnline:
		fmt.Println("User already online")
		goto retry
	case ErrUsernameExists:
		fmt.Println("User already online")
		goto retry
	case ErrInvalidCredentials:
		fmt.Println("Wrong username or password, try again")
		goto retry
	default:
		return User{}, err
	}
	return me, nil
}

func ChooseLoginOrRegister(userInput *bufio.Scanner) (LoginAction, error) {
	for {
		fmt.Println("Type r to register, l to login")

		userInput.Scan()
		err := userInput.Err()
		if err != nil {
			return ActionRegister, err
		}
		c := userInput.Text()
		switch c {
		case "r":
			return ActionRegister, nil
		case "l":
			return ActionLogin, nil
		default:
			continue
		}
	}
}

func promptForUsernameAndPassword(userInput *bufio.Scanner) (User, error) {
	fmt.Printf("Username: ")
	userInput.Scan()
	err := userInput.Err()
	if err != nil {
		return User{}, err
	}
	username := userInput.Text()

	fmt.Printf("Password: ")
	userInput.Scan()
	err = userInput.Err()
	if err != nil {
		return User{}, err
	}
	password := userInput.Text()

	return User{username, password}, nil
}

var ErrUsernameExists = errors.New("Username already exists")
var ErrUserOnline = errors.New("Username asready online")
var ErrInvalidCredentials = errors.New("Username asready online")

func login(action LoginAction, user User, serverConn io.ReadWriter) error {
	switch action {
	case ActionLogin:
		serverConn.Write([]byte("l\n"))
	case ActionRegister:
		serverConn.Write([]byte("r\n"))
	}

	serverConn.Write([]byte(user.name))
	serverConn.Write([]byte("\n"))
	serverConn.Write([]byte(user.password))
	serverConn.Write([]byte("\n"))

	serverOutput := bufio.NewScanner(serverConn)
	serverOutput.Scan()
	err := serverOutput.Err()

	if err != nil {
		return err
	}
	switch serverOutput.Text() {
	case "success":
		return nil
	case "exists":
		return ErrUsernameExists
	case "online":
		return ErrUserOnline
	case "invalidCredentials":
		return ErrInvalidCredentials
	default:
		return fmt.Errorf("Weird output from server: %s", serverOutput.Text())
	}
}
