package util

import (
	"bufio"
	"errors"
	"io"
	"log"
)

func ClosePrintErr(c io.Closer) {
	err := c.Close()
	if err != nil {
		log.Println(err)
	}
}

var ErrServerLoggedUsOut = errors.New("server logged us out")

var ErrClientHasQuit = io.EOF

type ReadInput struct {
	Val string
	Err error
}

func ReadAsyncIntoChan(scanner *bufio.Scanner) <-chan ReadInput {
	inputs := make(chan ReadInput)
	go func() {
		for {
			str, err := ScanLine(scanner)
			inputs <- ReadInput{str, err}
			if err != nil {
				return
			}
		}
	}()
	return inputs
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
