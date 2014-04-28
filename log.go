package rtmp

import (
	"log"
	"strings"
)

type LogLevel int

const (
	LogError LogLevel = iota
	LogWarn
	LogInfo
	LogDebug
	LogTrace
)

var logLevel LogLevel = LogInfo

func SetLogLevel(level LogLevel) {
	logLevel = level
}

type Logger interface {
	Errorf(string, ...interface{})
	Warnf(string, ...interface{})
	Infof(string, ...interface{})
	Debugf(string, ...interface{})
	Tracef(string, ...interface{})
	SetTag(string)
}

type logger struct {
	tag string
}

func NewLogger(tag string) Logger {
	return &logger{tag}
}

func logJoin(s ...string) string {
	return strings.Join(s, " - ")
}

func (l *logger) Errorf(format string, v ...interface{}) {
	if logLevel >= LogError {
		log.Printf(logJoin("error", l.tag, format), v...)
	}
}

func (l *logger) Warnf(format string, v ...interface{}) {
	if logLevel >= LogWarn {
		log.Printf(logJoin("warn", l.tag, format), v...)
	}
}

func (l *logger) Infof(format string, v ...interface{}) {
	if logLevel >= LogInfo {
		log.Printf(logJoin("info", l.tag, format), v...)
	}
}

func (l *logger) Debugf(format string, v ...interface{}) {
	if logLevel >= LogDebug {
		log.Printf(logJoin("debug", l.tag, format), v...)
	}
}

func (l *logger) Tracef(format string, v ...interface{}) {
	if logLevel >= LogTrace {
		log.Printf(logJoin("trace", l.tag, format), v)
	}
}

func (l *logger) SetTag(tag string) {
	l.tag = tag
}
