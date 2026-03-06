package fuse

import (
	"testing"
	"time"
)

func TestCacheGetSet(t *testing.T) {
	c := &Cache{entries: make(map[string]cacheEntry)}

	// Miss
	_, ok := c.Get("missing")
	if ok {
		t.Fatal("expected cache miss")
	}

	// Set and hit
	c.Set("key1", []byte("hello"), 10*time.Second)
	data, ok := c.Get("key1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(data) != "hello" {
		t.Errorf("got %q, want hello", data)
	}
}

func TestCacheExpiration(t *testing.T) {
	c := &Cache{entries: make(map[string]cacheEntry)}

	c.Set("expires", []byte("data"), 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	_, ok := c.Get("expires")
	if ok {
		t.Fatal("expected expired entry to be a miss")
	}
}

func TestCacheOverwrite(t *testing.T) {
	c := &Cache{entries: make(map[string]cacheEntry)}

	c.Set("key", []byte("v1"), 10*time.Second)
	c.Set("key", []byte("v2"), 10*time.Second)

	data, ok := c.Get("key")
	if !ok {
		t.Fatal("expected hit")
	}
	if string(data) != "v2" {
		t.Errorf("got %q, want v2", data)
	}
}

func TestCacheMultipleKeys(t *testing.T) {
	c := &Cache{entries: make(map[string]cacheEntry)}

	c.Set("a", []byte("1"), 10*time.Second)
	c.Set("b", []byte("2"), 10*time.Second)
	c.Set("c", []byte("3"), 10*time.Second)

	for _, tc := range []struct{ key, want string }{
		{"a", "1"}, {"b", "2"}, {"c", "3"},
	} {
		data, ok := c.Get(tc.key)
		if !ok {
			t.Errorf("key %q: expected hit", tc.key)
		}
		if string(data) != tc.want {
			t.Errorf("key %q: got %q, want %q", tc.key, data, tc.want)
		}
	}
}
