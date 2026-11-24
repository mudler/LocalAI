package bgjob

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// stallStdin fills the pipe of the job so that the next write blocks, as a
// child that does not read would.
func stallStdin(t *testing.T, j *Job) {
	t.Helper()
	_ = j.stdinW.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	_, err := j.stdinW.Write([]byte(strings.Repeat("x", 1<<20)))
	if err == nil {
		t.Fatal("the pipe did not fill")
	}
}

func TestRemoteInputAnswersBeforeTheAskerGivesUp(t *testing.T) {
	r := New(Options{InputTimeout: 10 * time.Second})
	j, err := r.Reserve(Spec{Owner: "alice", Chat: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	stallStdin(t, j)
	start := time.Now()
	rep := r.handle(context.Background(), Request{Action: ActionInput, Owner: "alice", Chat: "c1", ID: j.ID(), Input: "go\n"}, RemoteInputWait)
	if took := time.Since(start); took >= AskTimeout {
		t.Fatalf("the node answered after %v, the asker waits %v", took, AskTimeout)
	}
	if !errors.Is(rep.Err(), ErrInputTimeout) {
		t.Fatalf("reply %+v", rep)
	}
}
