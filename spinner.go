package main

import (
	"sync"
	"time"
)

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type Spinner struct {
	msg  string
	done chan struct{}
	wg   sync.WaitGroup
}

func startSpinner(msg string) *Spinner {
	s := &Spinner{
		msg:  msg,
		done: make(chan struct{}),
	}
	s.wg.Add(1)
	go s.run()
	return s
}

func (s *Spinner) run() {
	defer s.wg.Done()
	i := 0
	for {
		select {
		case <-s.done:
			stderrf("\r\033[K")
			return
		default:
			stderrf("\r%s %s", spinFrames[i%len(spinFrames)], s.msg)
			i++
			time.Sleep(80 * time.Millisecond)
		}
	}
}

func (s *Spinner) Stop() {
	close(s.done)
	s.wg.Wait()
}
