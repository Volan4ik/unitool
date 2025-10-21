package logger

import (
	"os"
	"github.com/rs/zerolog"
)

func New(appEnv string) zerolog.Logger {
	l := zerolog.New(os.Stdout).With().Timestamp().Logger()
	if appEnv == "dev" {
		zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	}
	return l
}