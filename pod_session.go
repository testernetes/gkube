package gkube

import (
	"sync"

	"github.com/onsi/gomega/gbytes"
)

type PodSession struct {
	//A *gbytes.Buffer connected to the command's stdout
	Out *gbytes.Buffer

	//A *gbytes.Buffer connected to the command's stderr
	Err *gbytes.Buffer

	lock     *sync.Mutex
	exitCode int
}

func (s *PodSession) ExitCode() int {
	return s.exitCode
}

func (s *PodSession) Buffer() *gbytes.Buffer {
	return s.Out
}
