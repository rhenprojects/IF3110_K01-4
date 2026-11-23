package workflow

import "sort"

// LeaderID kosong berarti group belum punya leader.
type Group struct {
	ID       string
	LeaderID string
}

type ApprovalResult struct {
	Approved bool
	Pending  []string // ID leader yang belum approve
	NoLeader []string // ID group tanpa leader
}

// Spec: perlu approval leader dari setiap group yang punya akses (view maupun edit).
func Evaluate(groups []Group, approvedBy []string) ApprovalResult {
	approved := make(map[string]bool, len(approvedBy))
	for _, id := range approvedBy {
		approved[id] = true
	}

	pending := make(map[string]bool)
	res := ApprovalResult{}
	for _, g := range groups {
		switch {
		case g.LeaderID == "":
			res.NoLeader = append(res.NoLeader, g.ID)
		case !approved[g.LeaderID]:
			pending[g.LeaderID] = true
		}
	}
	for id := range pending {
		res.Pending = append(res.Pending, id)
	}
	sort.Strings(res.Pending)
	sort.Strings(res.NoLeader)

	// Tanpa group tidak ada yang berhak approve: tolak (deny by default).
	res.Approved = len(groups) > 0 && len(res.Pending) == 0 && len(res.NoLeader) == 0
	return res
}

// Hanya leader dari group yang punya akses ke database ini yang boleh approve.
func CanApprove(userID string, groups []Group) bool {
	if userID == "" {
		return false
	}
	for _, g := range groups {
		if g.LeaderID == userID {
			return true
		}
	}
	return false
}
