package rtmp

type ConnError interface {
	Error() string
	Command() Command
	IsFatal() bool
}

type connError struct {
	error
	cmd   Command
	fatal bool
}

func NewConnError(err error, cmd Command, fatal bool) ConnError {
	return connError{err, cmd, fatal}
}

func NewConnErrorStatus(err error, status Status, fatal bool) ConnError {
	return connError{err, OnStatusCommand{Info: status}, fatal}
}

func (ce connError) Command() Command {
	return ce.cmd
}

func (ce connError) IsFatal() bool {
	return ce.fatal
}
