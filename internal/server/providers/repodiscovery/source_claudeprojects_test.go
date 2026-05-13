package repodiscovery

import (
	"context"
	"errors"
	"testing"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeprojects"
)

type fakeConversationLister struct {
	convs []claudeprojects.Conversation
	err   error
}

func (f *fakeConversationLister) List(_ context.Context) ([]claudeprojects.Conversation, error) {
	return f.convs, f.err
}

func strPtr(s string) *string { return &s }

func TestClaudeProjectsSource_Roots_ExtractsCwds(t *testing.T) {
	src := NewClaudeProjectsSource(&fakeConversationLister{
		convs: []claudeprojects.Conversation{
			{Cwd: strPtr("/Users/me/work/foo")},
			{Cwd: strPtr("/Users/me/work/bar")},
		},
	})
	got, err := src.Roots(context.Background())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if !equalSlices(got, []string{"/Users/me/work/foo", "/Users/me/work/bar"}) {
		t.Errorf("got %v", got)
	}
}

func TestClaudeProjectsSource_Roots_SkipsNilCwd(t *testing.T) {
	src := NewClaudeProjectsSource(&fakeConversationLister{
		convs: []claudeprojects.Conversation{
			{Cwd: nil},                          // older transcript without cwd
			{Cwd: strPtr("")},                   // empty cwd
			{Cwd: strPtr("/Users/me/work/baz")}, // valid
		},
	})
	got, err := src.Roots(context.Background())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if !equalSlices(got, []string{"/Users/me/work/baz"}) {
		t.Errorf("got %v, want [/Users/me/work/baz]", got)
	}
}

func TestClaudeProjectsSource_Roots_NilLister(t *testing.T) {
	src := NewClaudeProjectsSource(nil)
	got, err := src.Roots(context.Background())
	if err != nil || got != nil {
		t.Errorf("nil lister: got (%v, %v); want (nil, nil)", got, err)
	}
}

func TestClaudeProjectsSource_Roots_ListerError(t *testing.T) {
	src := NewClaudeProjectsSource(&fakeConversationLister{err: errors.New("boom")})
	_, err := src.Roots(context.Background())
	if err == nil {
		t.Errorf("expected error from lister to propagate")
	}
}
