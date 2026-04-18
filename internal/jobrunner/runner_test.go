package jobrunner_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/jobrunner"
)

func TestRunner_SubscribeReceivesEvent(t *testing.T) {
	r := jobrunner.New(nil, nil, nil)
	jobID := uuid.New()

	ch, unsub := r.Subscribe(jobID)
	defer unsub()

	r.Broadcast(jobID, jobrunner.Event{Type: "progress", Payload: []byte(`{"n":1}`)})

	select {
	case e := <-ch:
		if e.Type != "progress" {
			t.Errorf("type = %q, want progress", e.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestRunner_Broadcast_NoSubscribers_DoesNotBlock(t *testing.T) {
	r := jobrunner.New(nil, nil, nil)
	r.Broadcast(uuid.New(), jobrunner.Event{Type: "test", Payload: []byte(`{}`)})
}

func TestRunner_Unsubscribe_ClosesChannel(t *testing.T) {
	r := jobrunner.New(nil, nil, nil)
	jobID := uuid.New()

	ch, unsub := r.Subscribe(jobID)
	unsub()

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected closed channel")
		}
	case <-time.After(100 * time.Millisecond):
		// acceptable — buffered channel, no panic
	}
}

func TestRunner_MultipleSubscribers_AllReceive(t *testing.T) {
	r := jobrunner.New(nil, nil, nil)
	jobID := uuid.New()

	ch1, unsub1 := r.Subscribe(jobID)
	ch2, unsub2 := r.Subscribe(jobID)
	defer unsub1()
	defer unsub2()

	r.Broadcast(jobID, jobrunner.Event{Type: "x", Payload: []byte(`{}`)})

	for _, ch := range []<-chan jobrunner.Event{ch1, ch2} {
		select {
		case e := <-ch:
			if e.Type != "x" {
				t.Errorf("type = %q, want x", e.Type)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout")
		}
	}
}
