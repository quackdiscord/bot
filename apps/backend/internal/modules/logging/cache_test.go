package logging_test

import (
	"fmt"
	"sync"
	"testing"

	logmodule "github.com/quackdiscord/bot/internal/modules/logging"
)

func TestCacheConcurrentBoundedAndGuildScoped(t *testing.T) {
	cache := logmodule.NewMessageCache(10)
	cache.SetGuildLimit("a", 5)
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Put(logmodule.CachedMessage{GuildID: "a", MessageDiscordID: fmt.Sprint(i), Content: "a"})
			cache.Put(logmodule.CachedMessage{GuildID: "b", MessageDiscordID: fmt.Sprint(i), Content: "b"})
			cache.Get("a", fmt.Sprint(i))
		}()
	}
	wg.Wait()
	if cache.Len("a") != 5 || cache.Len("b") != 10 {
		t.Fatalf("cached a=%d b=%d, want 5 and 10", cache.Len("a"), cache.Len("b"))
	}
}
