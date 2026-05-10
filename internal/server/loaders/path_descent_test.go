package loaders

import "testing"

// TestCwdInWorktree_PrefixBypass is the regression test for #515: a worktree
// at /repo/foo must NOT match a cwd /repo/foobar. Strings.HasPrefix used to
// return true here; filepath.Rel correctly returns "../foobar" → reject.
func TestCwdInWorktree_PrefixBypass(t *testing.T) {
	cases := []struct {
		name         string
		cwd          string
		worktreePath string
		want         bool
	}{
		// Match cases.
		{"exact match", "/repo/foo", "/repo/foo", true},
		{"trailing-slash worktree", "/repo/foo", "/repo/foo/", true},
		{"trailing-slash cwd", "/repo/foo/", "/repo/foo", true},
		{"direct child", "/repo/foo/src", "/repo/foo", true},
		{"deep descendant", "/repo/foo/a/b/c", "/repo/foo", true},

		// Reject: classic prefix-bypass — worktree /repo/foo, cwd /repo/foobar.
		{"prefix-bypass sibling", "/repo/foobar", "/repo/foo", false},
		{"prefix-bypass deep", "/repo/foobar/deep", "/repo/foo", false},

		// Reject: cwd outside worktree.
		{"cwd above worktree", "/repo", "/repo/foo", false},
		{"cwd unrelated", "/elsewhere", "/repo/foo", false},

		// Empty inputs are not matches.
		{"empty cwd", "", "/repo/foo", false},
		{"empty worktree path", "/repo/foo", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cwdInWorktree(tc.cwd, tc.worktreePath)
			if got != tc.want {
				t.Errorf("cwdInWorktree(%q, %q) = %v, want %v",
					tc.cwd, tc.worktreePath, got, tc.want)
			}
		})
	}
}
