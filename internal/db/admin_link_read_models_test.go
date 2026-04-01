package db

import "testing"

func TestAdminOrgLinkCountDelta(t *testing.T) {
	tests := []struct {
		name  string
		delta int
		want  int
	}{
		{name: "create", delta: 1, want: 1},
		{name: "delete", delta: -1, want: -1},
		{name: "noop", delta: 0, want: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := AdminOrgLinkCountDelta(test.delta); got != test.want {
				t.Fatalf("delta = %d, want %d", got, test.want)
			}
		})
	}
}
