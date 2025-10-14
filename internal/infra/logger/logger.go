package logger

import (
	"log"
	"os"
)

func New() *log.Logger {
	l := log.New(os.Stdout, "[bot] ", log.LstdFlags|log.Lshortfile)
	return l
}