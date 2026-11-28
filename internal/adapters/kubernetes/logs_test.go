package kubernetes

import (
	"context"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

func TestCombineLogs(t *testing.T) {
	cases := []struct{ name, current, previous, want string }{
		{"both", "now", "before", "now\nbefore"},
		{"no previous run", "now", "", "now\n"},
		{"nothing", "", "", "\n"},
	}
	for _, c := range cases {
		if got := combineLogs(c.current, c.previous); got != c.want {
			t.Errorf("%s: want %q, got %q", c.name, c.want, got)
		}
	}
}

func TestCrashLogReadsCurrentAndPrevious(t *testing.T) {
	adapter := New(nil, nil, fake.NewClientset())
	got, err := adapter.CrashLog(context.Background(), "qdrant-0", "default", 50)
	if err != nil {
		t.Fatal(err)
	}
	if got != "fake logs\nfake logs" {
		t.Fatalf("got %q", got)
	}
}
