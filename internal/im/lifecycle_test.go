package im

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSubscriptionsCloseCancelsTrackedContexts(t *testing.T) {
	var s Subscriptions
	ctx1, release1, err := s.Track(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release1()
	ctx2, release2, err := s.Track(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release2()

	s.Close()
	for i, ctx := range []context.Context{ctx1, ctx2} {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Fatalf("tracked ctx %d was not cancelled by Close", i)
		}
	}
	if _, _, err := s.Track(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Track after Close err = %v, want ErrClosed", err)
	}
	s.Close() // idempotent
}

func TestSubscriptionsReleaseDoesNotAffectOthers(t *testing.T) {
	var s Subscriptions
	ctx1, release1, _ := s.Track(context.Background())
	release1()
	select {
	case <-ctx1.Done():
	case <-time.After(time.Second):
		t.Fatal("released subscription was not cancelled")
	}
	ctx2, release2, _ := s.Track(context.Background())
	defer release2()
	s.Close()
	select {
	case <-ctx2.Done():
	case <-time.After(time.Second):
		t.Fatal("Close missed the second subscription")
	}
}

func TestSubscriptionsRaceFree(t *testing.T) {
	var s Subscriptions
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, release, err := s.Track(context.Background())
			if err == nil {
				release()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Close()
	}()
	wg.Wait()
}
