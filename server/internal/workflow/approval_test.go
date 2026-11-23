package workflow

import (
	"reflect"
	"testing"
)

func TestEvaluate(t *testing.T) {
	cases := []struct {
		name         string
		groups       []Group
		approvedBy   []string
		wantApproved bool
		wantPending  []string
		wantNoLeader []string
	}{
		{
			name:         "satu group, leader sudah approve",
			groups:       []Group{{"g1", "u1"}},
			approvedBy:   []string{"u1"},
			wantApproved: true,
		},
		{
			name:        "satu group, leader belum approve",
			groups:      []Group{{"g1", "u1"}},
			wantPending: []string{"u1"},
		},
		{
			name:        "dua group, baru satu leader approve",
			groups:      []Group{{"g1", "u1"}, {"g2", "u2"}},
			approvedBy:  []string{"u1"},
			wantPending: []string{"u2"},
		},
		{
			name:         "dua group, kedua leader approve",
			groups:       []Group{{"g1", "u1"}, {"g2", "u2"}},
			approvedBy:   []string{"u1", "u2"},
			wantApproved: true,
		},
		{
			name:         "satu orang memimpin dua group, satu approval cukup",
			groups:       []Group{{"g1", "u1"}, {"g2", "u1"}},
			approvedBy:   []string{"u1"},
			wantApproved: true,
		},
		{
			name:         "group tanpa leader memblokir meski leader lain sudah approve",
			groups:       []Group{{"g1", "u1"}, {"g2", ""}},
			approvedBy:   []string{"u1"},
			wantNoLeader: []string{"g2"},
		},
		{
			name: "tidak ada group: ditolak",
		},
		{
			name:        "approval dari bukan leader tidak dihitung",
			groups:      []Group{{"g1", "u1"}},
			approvedBy:  []string{"u9"},
			wantPending: []string{"u1"},
		},
		{
			name:         "approval ganda dari orang yang sama tidak berbahaya",
			groups:       []Group{{"g1", "u1"}, {"g2", "u2"}},
			approvedBy:   []string{"u1", "u1", "u1"},
			wantPending:  []string{"u2"},
			wantApproved: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Evaluate(c.groups, c.approvedBy)
			if got.Approved != c.wantApproved {
				t.Errorf("Approved = %v, mau %v", got.Approved, c.wantApproved)
			}
			if !reflect.DeepEqual(got.Pending, c.wantPending) {
				t.Errorf("Pending = %v, mau %v", got.Pending, c.wantPending)
			}
			if !reflect.DeepEqual(got.NoLeader, c.wantNoLeader) {
				t.Errorf("NoLeader = %v, mau %v", got.NoLeader, c.wantNoLeader)
			}
		})
	}
}

func TestEvaluate_FormerLeaderApprovalDoesNotCount(t *testing.T) {
	// u1 sempat approve, lalu leader group diganti menjadi u2.
	groups := []Group{{"g1", "u2"}}
	got := Evaluate(groups, []string{"u1"})
	if got.Approved {
		t.Fatal("approval dari mantan leader tidak boleh dihitung")
	}
}

func TestCanApprove(t *testing.T) {
	groups := []Group{{"g1", "u1"}, {"g2", ""}}
	cases := []struct {
		userID string
		want   bool
	}{
		{"u1", true},
		{"u2", false},
		{"", false}, 
	}
	for _, c := range cases {
		if got := CanApprove(c.userID, groups); got != c.want {
			t.Errorf("CanApprove(%q) = %v, mau %v", c.userID, got, c.want)
		}
	}
	if CanApprove("u1", nil) {
		t.Error("tanpa group, tidak ada yang boleh approve")
	}
}