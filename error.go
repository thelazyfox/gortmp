package rtmp

import (
	"fmt"
)

type ErrorResponse interface {
	error
	Command() *Command
}

type errorResponse struct {
	cmd *Command
}

func (e *errorResponse) Command() *Command {
	return e.cmd
}

func (e *errorResponse) Error() string {
	return fmt.Sprintf("%#v", e.cmd)
}

func NewErrorResponse(cmd *Command) ErrorResponse {
	return &errorResponse{cmd}
}
