//go:build integration

// Integration test for free-text input (internal/gateway/input) on a real
// Redis: a waiting answer is taken once, only by the reply to its own
// question, however many deliveries race for it, and it expires on its own.
package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/gateway/input"
	infraredis "github.com/mrjvadi/torncity/internal/infrastructure/redis"
)

func TestInputIsTakenOnce(t *testing.T) {
	client := requireRedis(t)
	store := infraredis.NewInputStore(client)
	ctx := testCtx(t)
	key := input.Key{BotID: "it-" + randomToken(t, 8), ChatID: -100123, UserID: newTelegramUserID(t)}
	t.Cleanup(func() { _ = store.Drop(context.Background(), key) })

	value, err := input.Encode(input.Pending{Command: "bank.deposit", Field: "amount", Prompt: 555})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, key, value, time.Minute); err != nil {
		t.Fatal(err)
	}

	// A reply to some other message answers nothing, and leaves it waiting.
	if got, err := store.Take(ctx, key, 777); err != nil || got != "" {
		t.Fatalf("a reply to another message took %q (%v)", got, err)
	}

	// Ten deliveries of the right reply at once: exactly one takes it.
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		took int
	)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := store.Take(context.Background(), key, 555)
			if err != nil {
				t.Errorf("Take: %v", err)
				return
			}
			if got != "" {
				mu.Lock()
				took++
				mu.Unlock()
				if p, err := input.Decode(got); err != nil || p.Command != "bank.deposit" {
					t.Errorf("took %q (%v)", got, err)
				}
			}
		}()
	}
	wg.Wait()
	if took != 1 {
		t.Fatalf("the answer was taken %d times, want once", took)
	}

	// The private chat takes without a prompt; a dropped question is gone.
	if err := store.Put(ctx, key, value, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.Drop(ctx, key); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Take(ctx, key, 0); got != "" {
		t.Errorf("a dropped question was answered")
	}

	// It expires on its own.
	if err := store.Put(ctx, key, value, 150*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if got, _ := store.Take(ctx, key, 0); got != "" {
		t.Errorf("an expired question was answered")
	}

	// Asking is throttled: the second press inside the cooldown asks nothing.
	if ok, err := store.Arm(ctx, key, time.Second); err != nil || !ok {
		t.Fatalf("the first question was not armed: %v %v", ok, err)
	}
	if ok, _ := store.Arm(ctx, key, time.Second); ok {
		t.Error("a second question inside the cooldown was armed")
	}
}
