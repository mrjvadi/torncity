package faction

import (
	"errors"
	"testing"
)

func charter() Charter {
	return Charter{
		Officer: {Invite, Decide, Kick, Deposit, Withdraw, Plan, Launch, Join},
		Member:  {Deposit, Join},
	}
}

func TestCharter(t *testing.T) {
	c := charter()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if !c.Can(Leader, Link) {
		t.Error("the leader cannot link")
	}
	if c.Can(Member, Withdraw) {
		t.Error("a member may withdraw")
	}
	if err := (Charter{Member: {"steal"}}).Validate(); !errors.Is(err, ErrInvalidCharter) {
		t.Errorf("an unknown right accepted: %v", err)
	}
}

func TestKickAndRank(t *testing.T) {
	c := charter()
	if err := c.CheckKick(Officer, Member, false); err != nil {
		t.Errorf("an officer cannot kick a member: %v", err)
	}
	if err := c.CheckKick(Officer, Officer, false); !errors.Is(err, ErrOutranked) {
		t.Errorf("an officer kicked an officer: %v", err)
	}
	if err := c.CheckKick(Officer, Leader, false); !errors.Is(err, ErrOutranked) {
		t.Errorf("an officer kicked the leader: %v", err)
	}
	if err := c.CheckKick(Member, Member, false); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("a member kicked: %v", err)
	}
	if err := c.CheckRank(Leader, Member, Officer, false); err != nil {
		t.Errorf("the leader cannot promote: %v", err)
	}
	if err := c.CheckRank(Leader, Member, Leader, false); err == nil {
		t.Error("leadership passed by a promotion")
	}
	if err := c.CheckRank(Officer, Member, Officer, false); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("an officer promoted without the right: %v", err)
	}
}

func TestSplitCreatesNothing(t *testing.T) {
	s := Shares{Leader: 3, Officer: 2, Member: 1}
	for _, total := range []int64{0, 1, 7, 1000, 12_345, 999_999} {
		crew := []Rank{Officer, Member, Member, Leader}
		cut, each := Split(total, 1000, crew, s)
		sum := cut
		for _, e := range each {
			if e < 0 {
				t.Fatalf("a negative share %d", e)
			}
			sum += e
		}
		if sum != total {
			t.Errorf("total %d split into %d", total, sum)
		}
	}
	cut, each := Split(1000, 1000, []Rank{Leader, Member}, s)
	if cut != 100 || each[0] != 675 || each[1] != 225 {
		t.Errorf("split = %d, %v", cut, each)
	}
	_, even := Split(10, 0, []Rank{Member, Member}, Shares{Leader: 1})
	if even[0] != 5 || even[1] != 5 {
		t.Errorf("zero-weight crew split %v", even)
	}
}
