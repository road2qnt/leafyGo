package migration

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

func TestEvaluateQuorumIntersection(t *testing.T) {
	g1 := uuid.New()
	g2 := uuid.New()
	g3Revoked := uuid.New()
	g4Deleted := uuid.New()
	l1 := uuid.New()
	l2 := uuid.New()
	l3 := uuid.New()
	l4 := uuid.New()

	snapshot := []QuorumGroupState{
		{GroupID: g1, GroupName: "dba", SnapshotLeaderID: l1, HasCurrentAccess: true},
		{GroupID: g2, GroupName: "backend", SnapshotLeaderID: l2, HasCurrentAccess: true},
		{GroupID: g3Revoked, GroupName: "revoked-team", SnapshotLeaderID: l3, HasCurrentAccess: false},
		{GroupID: g4Deleted, GroupName: "deleted-team", SnapshotLeaderID: l4, HasCurrentAccess: true, IsGroupDeleted: true},
	}

	// 1. Only g1 approved -> 1 of 2 in intersection
	reqGroups, reqCount, appCount, err := EvaluateQuorumIntersection(snapshot, map[uuid.UUID]string{
		g1: domain.DecisionApproved,
	})
	if err != nil {
		t.Fatalf("EvaluateQuorumIntersection: %v", err)
	}
	if len(reqGroups) != 2 || reqCount != 2 || appCount != 1 {
		t.Fatalf("intersection = (len=%d, req=%d, app=%d); want (2, 2, 1)", len(reqGroups), reqCount, appCount)
	}

	// 2. Both g1 and g2 approved -> 2 of 2 in intersection (revoked g3 and deleted g4 do NOT block quorum)
	_, reqCount, appCount, err = EvaluateQuorumIntersection(snapshot, map[uuid.UUID]string{
		g1: domain.DecisionApproved,
		g2: domain.DecisionApproved,
	})
	if err != nil || reqCount != 2 || appCount != 2 {
		t.Fatalf("full quorum intersection = (req=%d, app=%d, err=%v); want (2, 2, nil)", reqCount, appCount, err)
	}

	// 3. Empty intersection (all groups had access revoked) -> ErrNoAccessGroups (422 NO_ACCESS_GROUPS)
	emptySnap := []QuorumGroupState{
		{GroupID: g3Revoked, GroupName: "revoked-team", SnapshotLeaderID: l3, HasCurrentAccess: false},
	}
	if _, _, _, err := EvaluateQuorumIntersection(emptySnap, nil); !errors.Is(err, ErrNoAccessGroups) {
		t.Fatalf("empty intersection err = %v; want ErrNoAccessGroups", err)
	}
}

func TestMatchActorLeaderGroupsSelfApprovalAndForbidden(t *testing.T) {
	g1 := uuid.New()
	g2 := uuid.New()
	proposerLeaderID := uuid.New()
	leader2ID := uuid.New()
	nonLeaderID := uuid.New()

	required := []QuorumGroupState{
		{GroupID: g1, GroupName: "backend", SnapshotLeaderID: proposerLeaderID, HasCurrentAccess: true},
		{GroupID: g2, GroupName: "analytics", SnapshotLeaderID: leader2ID, HasCurrentAccess: true},
	}

	// 1. Self-approval by proposer who is also an active Group Leader of g1 is allowed
	proposerLeader := domain.User{ID: proposerLeaderID, Username: "proposer_leader", IsActive: true}
	matched, err := MatchActorLeaderGroups(proposerLeader, required)
	if err != nil || len(matched) != 1 || matched[0] != g1 {
		t.Fatalf("self-approval by proposer-leader = (%v, %v); want [%v]", matched, err, g1)
	}

	// 2. Non-leader is rejected with ErrNotGroupLeader (403 FORBIDDEN_NOT_GROUP_LEADER)
	nonLeader := domain.User{ID: nonLeaderID, Username: "member", IsActive: true}
	if _, err := MatchActorLeaderGroups(nonLeader, required); !errors.Is(err, ErrNotGroupLeader) {
		t.Fatalf("non-leader err = %v; want ErrNotGroupLeader", err)
	}

	// 3. Platform Admin is rejected with ErrNotGroupLeader
	admin := domain.User{ID: proposerLeaderID, Username: "admin", IsPlatformAdmin: true, IsActive: true}
	if _, err := MatchActorLeaderGroups(admin, required); !errors.Is(err, ErrNotGroupLeader) {
		t.Fatalf("platform admin err = %v; want ErrNotGroupLeader", err)
	}

	// 4. Empty required groups returns ErrNoAccessGroups (422 NO_ACCESS_GROUPS)
	if _, err := MatchActorLeaderGroups(proposerLeader, nil); !errors.Is(err, ErrNoAccessGroups) {
		t.Fatalf("empty required groups err = %v; want ErrNoAccessGroups", err)
	}
}

func TestCheckPredecessorGate(t *testing.T) {
	// 1. Apply on slot 2 when slot 1 is still draft/pending_review -> 409 PREDECESSOR_NOT_READY
	err := CheckPredecessorGate(domain.ActionApply, 2, []SlotReadinessState{
		{EffectiveSeq: 1, IsActive: false, HasQueuedOrRunningApply: false},
		{EffectiveSeq: 2, IsActive: false, HasQueuedOrRunningApply: false},
	})
	if !errors.Is(err, ErrPredecessorNotReady) {
		t.Fatalf("out-of-order apply slot 2 err = %v; want ErrPredecessorNotReady", err)
	}

	// 2. Apply on slot 2 when slot 1 is Active -> passes
	err = CheckPredecessorGate(domain.ActionApply, 2, []SlotReadinessState{
		{EffectiveSeq: 1, IsActive: true},
		{EffectiveSeq: 2, IsActive: false},
	})
	if err != nil {
		t.Fatalf("apply slot 2 with slot 1 active failed: %v", err)
	}

	// 3. Apply on slot 2 when slot 1 has a queued/running apply job -> passes
	err = CheckPredecessorGate(domain.ActionApply, 2, []SlotReadinessState{
		{EffectiveSeq: 1, IsActive: false, HasQueuedOrRunningApply: true},
		{EffectiveSeq: 2, IsActive: false},
	})
	if err != nil {
		t.Fatalf("apply slot 2 with slot 1 queued failed: %v", err)
	}

	// 4. Rollback on slot 1 while slot 2 is Active and has no queued/running rollback -> 409 PREDECESSOR_NOT_READY
	err = CheckPredecessorGate(domain.ActionRollback, 1, []SlotReadinessState{
		{EffectiveSeq: 1, IsActive: true},
		{EffectiveSeq: 2, IsActive: true, HasQueuedOrRunningRevert: false},
	})
	if !errors.Is(err, ErrPredecessorNotReady) {
		t.Fatalf("out-of-order rollback slot 1 err = %v; want ErrPredecessorNotReady", err)
	}

	// 5. Rollback on slot 2 (highest Active effective_seq) -> passes
	err = CheckPredecessorGate(domain.ActionRollback, 2, []SlotReadinessState{
		{EffectiveSeq: 1, IsActive: true},
		{EffectiveSeq: 2, IsActive: true},
	})
	if err != nil {
		t.Fatalf("LIFO rollback on highest active slot 2 failed: %v", err)
	}

	// 6. Rollback on slot 1 when slot 2 already has a queued/running rollback job -> passes
	err = CheckPredecessorGate(domain.ActionRollback, 1, []SlotReadinessState{
		{EffectiveSeq: 1, IsActive: true},
		{EffectiveSeq: 2, IsActive: true, HasQueuedOrRunningRevert: true},
	})
	if err != nil {
		t.Fatalf("rollback slot 1 with slot 2 rollback already queued failed: %v", err)
	}
}

