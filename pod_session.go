package gkube

import (
	"sync"

	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	. "github.com/onsi/gomega/gexec"
)

type PodSession struct {
	//A *gbytes.Buffer connected to the command's stdout
	Out *gbytes.Buffer

	//A *gbytes.Buffer connected to the command's stderr
	Err *gbytes.Buffer

	// the exit code
	Code int

	//A channel that will close when the command exits
	Exited <-chan struct{}

	lock     *sync.Mutex
	exitCode int
}

func (s *PodSession) ExitCode() int {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.exitCode
}

func (s *PodSession) Buffer() *gbytes.Buffer {
	return s.Out
}

func (s *PodSession) Wait(timeout ...interface{}) *PodSession {
	EventuallyWithOffset(1, s, timeout...).Should(Exit())
	return s
}
