package log

import (
	"log"
)

const (
	ERROR   = 1
	WARNING = 2
	INFO    = 3
	DEBUG   = 4
	TRACE   = 5
)

var logLevel int = INFO

func SetLogLevel(level int) {
	logLevel = level
}

func Warning(format string, v ...interface{}) {
	if logLevel >= WARNING {
		log.Printf(format, v...)
	}
}

func Error(format string, v ...interface{}) {
	if logLevel >= ERROR {
		log.Printf(format, v...)
	}
}

func Info(format string, v ...interface{}) {
	if logLevel >= INFO {
		log.Printf(format, v...)
	}
}

func Debug(format string, v ...interface{}) {
	if logLevel >= DEBUG {
		log.Printf(format, v...)
	}
}

func Trace(format string, v ...interface{}) {
	if logLevel >= TRACE {
		log.Printf(format, v...)
	}
}
