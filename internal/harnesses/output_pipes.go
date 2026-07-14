package harnesses

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// HarnessOutputPipes owns the parent ends of a subprocess harness's stdout
// and stderr pipes. The write ends are assigned directly to cmd instead of
// using exec.Cmd.StdoutPipe or StderrPipe: processlifecycle waits for its
// trusted supervisor in the background, and exec.Cmd.Wait is allowed to close
// descriptors created by those convenience methods before readers finish.
type HarnessOutputPipes struct {
	Stdout *os.File
	Stderr *os.File

	stdoutWriter *os.File
	stderrWriter *os.File
}

// PrepareHarnessOutputPipes attaches caller-owned stdout and stderr pipes to
// cmd. Call ReleaseWriters immediately after a successful StartBatch, and
// defer Close on every path.
func PrepareHarnessOutputPipes(cmd *exec.Cmd) (*HarnessOutputPipes, error) {
	if cmd == nil {
		return nil, fmt.Errorf("prepare harness output pipes: nil command")
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("prepare harness stdout pipe: %w", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		return nil, fmt.Errorf("prepare harness stderr pipe: %w", err)
	}
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter
	return &HarnessOutputPipes{
		Stdout:       stdoutReader,
		Stderr:       stderrReader,
		stdoutWriter: stdoutWriter,
		stderrWriter: stderrWriter,
	}, nil
}

// ReleaseWriters drops the embedding process's copies after StartBatch has
// inherited them. EOF then arrives as soon as the supervisor and its child
// close their copies.
func (p *HarnessOutputPipes) ReleaseWriters() error {
	if p == nil {
		return nil
	}
	stdoutWriter := p.stdoutWriter
	stderrWriter := p.stderrWriter
	p.stdoutWriter = nil
	p.stderrWriter = nil
	var stdoutErr, stderrErr error
	if stdoutWriter != nil {
		stdoutErr = stdoutWriter.Close()
	}
	if stderrWriter != nil {
		stderrErr = stderrWriter.Close()
	}
	return errors.Join(stdoutErr, stderrErr)
}

// Close releases every pipe end still owned by the embedding process.
func (p *HarnessOutputPipes) Close() error {
	if p == nil {
		return nil
	}
	writersErr := p.ReleaseWriters()
	var stdoutErr, stderrErr error
	if p.Stdout != nil {
		stdoutErr = p.Stdout.Close()
		p.Stdout = nil
	}
	if p.Stderr != nil {
		stderrErr = p.Stderr.Close()
		p.Stderr = nil
	}
	return errors.Join(writersErr, stdoutErr, stderrErr)
}
