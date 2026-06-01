package logger

import (
	"io"
	"log"
	"os"
)

var (
	Info  *log.Logger
	Error *log.Logger
)

func Init(level string) {
	Error = log.New(os.Stderr, "", log.LstdFlags)

	switch level {
	case "info":
		Info = log.New(os.Stderr, "", log.LstdFlags)
	default:
		Info = log.New(io.Discard, "", 0)
	}
}
