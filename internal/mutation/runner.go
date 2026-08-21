package mutation

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

type commandRunner interface {
	Run(context.Context, string, string, []string, io.Writer) (commandResult, error)
}

type commandResult struct {
	ExitCode   int
	OutputTail string
}

type execRunner struct{}

const capturedOutputLimit = 4000

func (execRunner) Run(ctx context.Context, root, name string, args []string, output io.Writer) (commandResult, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = root
	configureCommandCancellation(command)
	captured := &tailBuffer{limit: capturedOutputLimit}
	command.Stdout = io.MultiWriter(output, captured)
	command.Stderr = io.MultiWriter(output, captured)
	if err := command.Run(); err != nil {
		message := captured.String()
		if exitError, ok := err.(*exec.ExitError); ok {
			return commandResult{ExitCode: exitError.ExitCode(), OutputTail: message}, nil
		}
		return commandResult{}, fmt.Errorf("%s failed: %w\n%s", name, err, message)
	}
	return commandResult{ExitCode: 0, OutputTail: captured.String()}, nil
}

type tailBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (buffer *tailBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	written := len(data)
	if len(data) >= buffer.limit {
		buffer.data = append(buffer.data[:0], data[len(data)-buffer.limit:]...)
		return written, nil
	}
	overflow := len(buffer.data) + len(data) - buffer.limit
	if overflow > 0 {
		copy(buffer.data, buffer.data[overflow:])
		buffer.data = buffer.data[:len(buffer.data)-overflow]
	}
	buffer.data = append(buffer.data, data...)
	return written, nil
}

func (buffer *tailBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.data)
}
