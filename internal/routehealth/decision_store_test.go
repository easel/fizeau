package routehealth

import (
	"testing"
	"time"
)

func TestDecisionStoreLatestDecisionAndTimestamp(t *testing.T) {
	type decision struct {
		value string
	}

	store := NewDecisionStore[decision]()
	firstAt := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store.Store("  route-a  ", decision{value: "first"}, firstAt)

	first, ok := store.Lookup(" route-a ")
	if !ok {
		t.Fatal("trimmed route key was not found")
	}
	if first.Decision != (decision{value: "first"}) {
		t.Fatalf("first decision = %#v, want first", first.Decision)
	}
	if !first.At.Equal(firstAt) {
		t.Fatalf("first timestamp = %v, want exact supplied timestamp %v", first.At, firstAt)
	}

	latestAt := firstAt.Add(time.Minute)
	store.Store("route-a", decision{value: "latest"}, latestAt)
	latest, ok := store.Lookup("route-a")
	if !ok {
		t.Fatal("replaced route key was not found")
	}
	if latest.Decision != (decision{value: "latest"}) || !latest.At.Equal(latestAt) {
		t.Fatalf("latest entry = %#v, want latest decision at %v", latest, latestAt)
	}

	before := time.Now()
	store.Store("route-zero-time", decision{value: "populated"}, time.Time{})
	after := time.Now()
	populated, ok := store.Lookup("route-zero-time")
	if !ok {
		t.Fatal("zero-time entry was not stored")
	}
	if populated.At.IsZero() || populated.At.Before(before) || populated.At.After(after) {
		t.Fatalf("zero-time entry timestamp = %v, want populated between %v and %v", populated.At, before, after)
	}

	store.Store("   ", decision{value: "blank"}, latestAt)
	if _, ok := store.Lookup(" "); ok {
		t.Fatal("blank route key was stored")
	}

	var nilStore *DecisionStore[decision]
	nilStore.Store("route-a", decision{value: "ignored"}, latestAt)
	if _, ok := nilStore.Lookup("route-a"); ok {
		t.Fatal("nil store returned an entry")
	}
}
