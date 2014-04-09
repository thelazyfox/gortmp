package rtmp

type ErrorCommand interface {
	Error() string
	Command() *Command
}

type ErrorStatus interface {
	Error() string
	Status() Status
}

type errorCommand struct {
	error
	cmd *Command
}

type errorStatus struct {
	error
	status Status
}

func NewErrorCommand(err error, cmd *Command) ErrorCommand {
	return &errorCommand{err, cmd}
}

func NewErrorStatus(err error, status Status) ErrorStatus {
	return &errorStatus{err, status}
}

func (ec *errorCommand) Command() *Command {
	return ec.cmd
}

func (es *errorStatus) Status() Status {
	return es.status
}
