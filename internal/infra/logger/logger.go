package logger

import (
	"log"
	"os"
)

func New() *log.Logger {
	return log.New(os.Stdout, "[bot] ", log.LstdFlags|log.Lshortfile)
}